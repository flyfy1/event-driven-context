package localcollection

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"event-driven-context/internal/v2"
	"event-driven-context/internal/v2client"
)

type FileMetadata struct {
	Version         int                 `json:"version"`
	Server          string              `json:"server"`
	ProjectID       string              `json:"project_id"`
	ThroughSequence int64               `json:"through_sequence"`
	File            v2.FileCatalogEntry `json:"file"`
	LocalFilename   string              `json:"local_filename"`
	Downloaded      bool                `json:"downloaded"`
	VerifiedSHA256  string              `json:"verified_sha256,omitempty"`
}
type FileResult struct {
	FileID       string `json:"file_id"`
	MetadataPath string `json:"metadata_path"`
	LocalPath    string `json:"local_path,omitempty"`
	Downloaded   bool   `json:"downloaded"`
	Cached       bool   `json:"cached"`
}
type SyncResult struct {
	Metadata    int      `json:"metadata"`
	Downloaded  int      `json:"downloaded"`
	Cached      int      `json:"cached"`
	Unavailable int      `json:"unavailable"`
	Conflicting int      `json:"conflicting"`
	Pending     int      `json:"pending"`
	Manifest    Manifest `json:"manifest"`
}

// SafeFilename is deterministic and cannot alias metadata or platform device
// names. The original display name remains unchanged in metadata.
func SafeFilename(display string) string {
	base := path.Base(strings.ReplaceAll(display, "\\", "/"))
	var b strings.Builder
	for _, r := range base {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	safe := strings.Trim(b.String(), ". _")
	if safe == "" {
		safe = "content"
	}
	if len(safe) > 120 {
		ext := path.Ext(safe)
		if len(ext) > 16 {
			ext = ""
		}
		suffix := "-" + digest([]byte(display))[:10]
		safe = safe[:120-len(suffix)-len(ext)] + suffix + ext
	}
	return "original-" + safe
}

func (c *Collection) SyncFiles(ctx context.Context, client *v2client.Client, all bool) (result SyncResult, err error) {
	defer func() { result.Manifest = c.manifest }()
	if err = c.checkClient(client); err != nil {
		return result, err
	}
	catalog := c.manifest.Catalog
	resume := catalog.Started && !catalog.Complete && catalog.ThroughSequence >= c.manifest.Notes.ThroughSequence
	in := v2.FileCatalogInput{Limit: 100}
	if resume {
		in.ThroughSequence = &catalog.ThroughSequence
		if !all {
			in.Cursor = catalog.Cursor
		}
	}
	first := true
	seenCursors := map[string]bool{}
	for {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		page, pageErr := client.ListFiles(ctx, c.manifest.ProjectID, in)
		if pageErr != nil {
			return result, pageErr
		}
		if page.ThroughSequence < c.manifest.Notes.ThroughSequence {
			return result, fmt.Errorf("catalog boundary is older than synced notes coverage")
		}
		if first {
			first = false
			c.manifest.Catalog = CatalogCoverage{Started: true, ThroughSequence: page.ThroughSequence, Cursor: in.Cursor}
			if all {
				c.manifest.Bytes = BytesCoverage{ThroughSequence: page.ThroughSequence}
			}
			if err = c.saveManifest(); err != nil {
				return result, err
			}
		}
		// Every subsequent request explicitly carries the same event boundary.
		boundary := c.manifest.Catalog.ThroughSequence
		if page.ThroughSequence != boundary {
			return result, fmt.Errorf("catalog snapshot changed during pagination")
		}
		for _, entry := range page.Files {
			fileResult, fileErr := c.syncFile(ctx, client, entry, boundary, all)
			if fileErr != nil {
				var conflict *ConflictError
				if errors.As(fileErr, &conflict) {
					result.Conflicting++
				} else {
					result.Unavailable++
				}
				result.Pending++
				return result, fmt.Errorf("cache %s: %w", entry.ID, fileErr)
			}
			result.Metadata++
			if fileResult.Downloaded {
				result.Downloaded++
			} else if fileResult.Cached {
				result.Cached++
			} else {
				result.Pending++
			}
		}
		c.manifest.Catalog.Cursor = page.NextCursor
		c.manifest.Catalog.Complete = page.NextCursor == ""
		if page.NextCursor == "" && all {
			c.manifest.Bytes = BytesCoverage{ThroughSequence: boundary, Complete: true}
		}
		if err = c.saveManifest(); err != nil {
			return result, err
		}
		if page.NextCursor == "" {
			return result, nil
		}
		if page.NextCursor == in.Cursor || seenCursors[page.NextCursor] {
			return result, fmt.Errorf("catalog returned repeated cursor")
		}
		seenCursors[page.NextCursor] = true
		in = v2.FileCatalogInput{Limit: 100, Cursor: page.NextCursor, ThroughSequence: &boundary}
	}
}

func (c *Collection) GetFile(ctx context.Context, client *v2client.Client, fileID string) (FileResult, error) {
	if err := c.checkClient(client); err != nil {
		return FileResult{}, err
	}
	if err := v2client.ValidateFileID(fileID); err != nil {
		return FileResult{}, err
	}
	// Freeze a fresh boundary even when the collection's catalog is older or
	// incomplete. One cached file never marks the whole catalog/bytes complete.
	page, err := client.ListFiles(ctx, c.manifest.ProjectID, v2.FileCatalogInput{Limit: 1})
	if err != nil {
		return FileResult{}, err
	}
	entry, err := client.FileMetadata(ctx, c.manifest.ProjectID, fileID, &page.ThroughSequence)
	if err != nil {
		return FileResult{}, err
	}
	return c.syncFile(ctx, client, entry, page.ThroughSequence, true)
}
func (c *Collection) checkClient(client *v2client.Client) error {
	if client == nil || CanonicalOrigin(client.BaseURL) != c.manifest.Server {
		return fmt.Errorf("client does not match collection server")
	}
	return nil
}
func (c *Collection) syncFile(ctx context.Context, client *v2client.Client, entry v2.FileCatalogEntry, through int64, wantBytes bool) (result FileResult, err error) {
	if err = v2client.ValidateFileCatalogEntry(c.manifest.ProjectID, entry, &through); err != nil {
		return result, err
	}
	dir := "files/" + entry.ID
	metadataPath := dir + "/metadata.json"
	filename := SafeFilename(entry.Filename)
	bytesPath := dir + "/" + filename
	result = FileResult{FileID: entry.ID, MetadataPath: filepath.Join(c.directory, filepath.FromSlash(metadataPath))}
	var old FileMetadata
	exists, err := c.readRecord(metadataPath, &old)
	if err != nil {
		return result, err
	}
	if exists {
		if old.Version != 1 || old.Server != c.manifest.Server || old.ProjectID != c.manifest.ProjectID || old.File.ID != entry.ID || old.LocalFilename != SafeFilename(old.File.Filename) || old.ThroughSequence < 0 {
			return result, &ConflictError{metadataPath, "metadata identity or cache path does not match"}
		}
		if err = v2client.ValidateFileCatalogEntry(c.manifest.ProjectID, old.File, &old.ThroughSequence); err != nil {
			return result, &ConflictError{metadataPath, "invalid stored file metadata"}
		}
		if old.File.SHA256 != entry.SHA256 || old.File.SizeBytes != entry.SizeBytes || old.LocalFilename != filename {
			return result, &ConflictError{metadataPath, "immutable remote file metadata changed"}
		}
		if old.Downloaded && old.VerifiedSHA256 != entry.SHA256 || !old.Downloaded && old.VerifiedSHA256 != "" {
			return result, &ConflictError{metadataPath, "invalid download registration"}
		}
	}
	if err = c.checkPath(bytesPath, false); err != nil {
		return result, err
	}
	cached, err := c.verifiedBytes(bytesPath, entry)
	if err != nil {
		c.manifest.Bytes.Complete = false
		_ = c.saveManifest()
		return result, err
	}
	if exists && old.Downloaded && !cached {
		c.manifest.Bytes.Complete = false
		if err = c.saveManifest(); err != nil {
			return result, err
		}
	}
	current := FileMetadata{Version: 1, Server: c.manifest.Server, ProjectID: c.manifest.ProjectID, ThroughSequence: through, File: entry, LocalFilename: filename, Downloaded: cached}
	if exists && old.ThroughSequence > through {
		current = old
		current.Downloaded = cached
		current.VerifiedSHA256 = ""
	}
	if cached {
		current.VerifiedSHA256 = entry.SHA256
		result.Cached = true
		result.LocalPath = filepath.Join(c.directory, filepath.FromSlash(bytesPath))
	}
	// Register known metadata before attempting bytes so interrupted downloads
	// remain discoverable, while Downloaded stays false until verification.
	if err = c.writeRecord(metadataPath, current); err != nil {
		return result, err
	}
	if !wantBytes || cached {
		return result, nil
	}
	if err = c.root.MkdirAll(dir, 0700); err != nil {
		return result, err
	}
	file, temp, err := c.tempFile(dir)
	if err != nil {
		return result, err
	}
	defer c.root.Remove(temp)
	info, downloadErr := client.GetFile(ctx, c.manifest.ProjectID, entry.ID, file)
	if downloadErr == nil && (info.SHA256 != entry.SHA256 || info.SizeBytes != entry.SizeBytes) {
		downloadErr = fmt.Errorf("download does not match catalog checksum or size")
	}
	if downloadErr == nil {
		downloadErr = file.Sync()
	}
	if closeErr := file.Close(); downloadErr == nil {
		downloadErr = closeErr
	}
	if downloadErr != nil {
		return result, downloadErr
	}
	if err = c.checkPath(bytesPath, false); err != nil {
		return result, err
	}
	// Link installs the verified inode without replacing a file created while
	// the download was in flight. An identical racing file can be adopted.
	if err = c.root.Link(temp, bytesPath); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return result, err
		}
		if verified, verifyErr := c.verifiedBytes(bytesPath, entry); verifyErr != nil {
			return result, verifyErr
		} else if !verified {
			return result, &ConflictError{bytesPath, "file disappeared during download"}
		}
		result.Cached = true
	} else {
		result.Downloaded = true
	}
	if err = c.syncDirectory(dir); err != nil {
		return result, err
	}
	current.Downloaded = true
	current.VerifiedSHA256 = entry.SHA256
	if err = c.writeRecord(metadataPath, current); err != nil {
		return result, err
	}
	result.LocalPath = filepath.Join(c.directory, filepath.FromSlash(bytesPath))
	return result, nil
}
func (c *Collection) verifiedBytes(name string, entry v2.FileCatalogEntry) (bool, error) {
	if err := c.checkPath(name, false); err != nil {
		return false, err
	}
	f, err := c.root.Open(name)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return false, err
	}
	if !st.Mode().IsRegular() || st.Size() != entry.SizeBytes {
		return false, &ConflictError{name, "existing bytes differ from catalog"}
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(f, entry.SizeBytes+1))
	if err != nil {
		return false, err
	}
	if n != entry.SizeBytes || fmt.Sprintf("%x", hash.Sum(nil)) != entry.SHA256 {
		return false, &ConflictError{name, "existing bytes differ from catalog"}
	}
	return true, nil
}
