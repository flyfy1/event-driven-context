package v2

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"event-driven-context/internal/core"
)

const maxImagePixels = 50_000_000

func (s *Service) PutFile(ctx context.Context, projectID string, in FileUpload) (FileInfo, error) {
	if err := s.identity.RequireProjectMember(ctx, projectID); err != nil {
		return FileInfo{}, err
	}
	if in.Reader == nil || in.SizeBytes < 1 || in.SizeBytes > MaxFileBytes {
		return FileInfo{}, v2err("too_large", "file size must be 1 to 50 MiB")
	}
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(in.MediaType))
	if err != nil {
		return FileInfo{}, v2err("unsupported_media_type", "invalid media type")
	}
	mediaType = strings.ToLower(mediaType)
	filename := strings.TrimSpace(in.Filename)
	if filename == "." || filename == ".." || filename == "" || len(filename) > 240 || !utf8.ValidString(filename) || strings.ContainsAny(filename, "/\\") {
		return FileInfo{}, v2err("invalid_input", "invalid filename")
	}
	for _, r := range filename {
		if r < 0x20 || r == 0x7f {
			return FileInfo{}, v2err("invalid_input", "invalid filename")
		}
	}
	data, err := io.ReadAll(io.LimitReader(in.Reader, MaxFileBytes+1))
	if err != nil {
		return FileInfo{}, err
	}
	if int64(len(data)) != in.SizeBytes {
		return FileInfo{}, v2err("invalid_input", "file size does not match")
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	if in.SHA256 != "" && (len(in.SHA256) != 64 || !strings.EqualFold(in.SHA256, digest)) {
		return FileInfo{}, v2err("invalid_input", "file sha256 does not match")
	}
	if !validMedia(mediaType, data) {
		return FileInfo{}, v2err("unsupported_media_type", "declared media type does not match allowed content")
	}
	idSum := sha256.Sum256([]byte(projectID + "\x00" + digest))
	id := "file_" + hex.EncodeToString(idSum[:16])
	info := FileInfo{ID: id, ProjectID: projectID, Filename: filename, MediaType: mediaType, SizeBytes: int64(len(data)), SHA256: digest, UploadedAt: nowUTC()}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.projectLocked(projectID)
	if existing, ok := p.Files[id]; ok {
		stored, verifyErr := s.openVerifiedFileLocked(existing)
		if verifyErr != nil {
			return FileInfo{}, verifyErr
		}
		_ = stored.Close()
		return existing, nil
	}
	path := filepath.Join(s.root, "files", id)
	if err = atomicWrite(path, data, 0600); err != nil {
		return FileInfo{}, err
	}
	candidate, err := cloneSnapshot(s.data)
	if err != nil {
		return FileInfo{}, err
	}
	cp := candidate.Projects[projectID]
	if cp == nil {
		cp = &projectData{}
		normalizeProjectData(cp)
		candidate.Projects[projectID] = cp
	}
	cp.Files[id] = info
	if err = s.persistSnapshotLocked(candidate); err != nil {
		return FileInfo{}, err
	}
	s.data = candidate
	return info, nil
}

func validMedia(mt string, data []byte) bool {
	switch mt {
	case "audio/mp4", "audio/mpeg", "audio/wav":
		return core.ValidateAudioContent(mt, data)
	case "image/jpeg":
		return validImage(data, "jpeg") && validJPEGStructure(data)
	case "image/png":
		return validImage(data, "png") && validPNGStructure(data)
	default:
		return strings.HasPrefix(mt, "text/") && utf8.Valid(data) && !bytes.Contains(data, []byte{0})
	}
}

func validImage(data []byte, expectedFormat string) bool {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format != expectedFormat || config.Width <= 0 || config.Height <= 0 || uint64(config.Width)*uint64(config.Height) > maxImagePixels {
		return false
	}
	_, format, err = image.Decode(bytes.NewReader(data))
	return err == nil && format == expectedFormat
}

func validPNGStructure(data []byte) bool {
	if len(data) < 33 || !bytes.Equal(data[:8], []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
		return false
	}
	offset := 8
	seenHeader, seenData := false, false
	for offset+12 <= len(data) {
		n := uint64(binary.BigEndian.Uint32(data[offset : offset+4]))
		end := uint64(offset) + 12 + n
		if end > uint64(len(data)) {
			return false
		}
		kind := string(data[offset+4 : offset+8])
		payloadEnd := offset + 8 + int(n)
		want := binary.BigEndian.Uint32(data[payloadEnd : payloadEnd+4])
		if crc32.ChecksumIEEE(data[offset+4:payloadEnd]) != want {
			return false
		}
		if !seenHeader {
			if kind != "IHDR" || n != 13 {
				return false
			}
			seenHeader = true
		}
		if kind == "IDAT" && n > 0 {
			seenData = true
		}
		offset = int(end)
		if kind == "IEND" {
			return n == 0 && seenHeader && seenData && offset == len(data)
		}
	}
	return false
}

func validJPEGStructure(data []byte) bool {
	if len(data) < 10 || data[0] != 0xff || data[1] != 0xd8 || data[len(data)-2] != 0xff || data[len(data)-1] != 0xd9 {
		return false
	}
	offset := 2
	for offset+4 <= len(data)-2 {
		if data[offset] != 0xff {
			return false
		}
		for offset < len(data) && data[offset] == 0xff {
			offset++
		}
		if offset >= len(data) {
			return false
		}
		marker := data[offset]
		offset++
		if marker == 0xd9 {
			return false
		}
		if marker >= 0xd0 && marker <= 0xd7 {
			continue
		}
		if offset+2 > len(data) {
			return false
		}
		n := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		if n < 2 || offset+n > len(data) {
			return false
		}
		if marker == 0xda {
			return offset+n < len(data)-2
		}
		offset += n
	}
	return false
}
func (s *Service) OpenFile(ctx context.Context, projectID, fileID string) (FileInfo, io.ReadCloser, error) {
	if err := s.identity.RequireProjectMember(ctx, projectID); err != nil {
		return FileInfo{}, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.data.Projects[projectID]
	if p == nil {
		return FileInfo{}, nil, core.ErrNotFound
	}
	return s.openFileLocked(p, fileID)
}
func (s *Service) openFileLocked(p *projectData, fileID string) (FileInfo, io.ReadCloser, error) {
	info, ok := p.Files[fileID]
	if !ok {
		return FileInfo{}, nil, core.ErrNotFound
	}
	f, err := s.openVerifiedFileLocked(info)
	return info, f, err
}

func (s *Service) openVerifiedFileLocked(info FileInfo) (*os.File, error) {
	path := filepath.Join(s.root, "files", info.ID)
	stat, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, core.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() || stat.Size() != info.SizeBytes {
		return nil, v2err("conflict", "stored file failed integrity check")
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, core.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	n, copyErr := io.Copy(h, f)
	if copyErr != nil || n != info.SizeBytes || !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), info.SHA256) {
		_ = f.Close()
		return nil, v2err("conflict", "stored file failed integrity check")
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}
func fileReferenced(p *projectData, fileID string) bool {
	for _, e := range p.Events {
		if e.Content.Kind == "file" && e.Content.FileID == fileID {
			return true
		}
	}
	return false
}

func (s *Service) CleanupUnreferencedFiles(ctx context.Context, before time.Time) (CleanupResult, error) {
	if before.IsZero() {
		return CleanupResult{}, v2err("invalid_input", "cleanup cutoff required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate, err := cloneSnapshot(s.data)
	if err != nil {
		return CleanupResult{}, err
	}
	ids := []string{}
	known := map[string]bool{}
	for _, p := range candidate.Projects {
		for id, info := range p.Files {
			known[id] = true
			uploaded, e := time.Parse(time.RFC3339Nano, info.UploadedAt)
			if e == nil && uploaded.Before(before) && !fileReferenced(p, id) {
				delete(p.Files, id)
				ids = append(ids, id)
			}
		}
	}
	entries, readErr := os.ReadDir(filepath.Join(s.root, "files"))
	if readErr != nil {
		return CleanupResult{}, readErr
	}
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasPrefix(entry.Name(), "file_") && !known[entry.Name()] {
			info, e := entry.Info()
			if e == nil && info.ModTime().Before(before) {
				ids = append(ids, entry.Name())
			}
		}
	}
	if len(ids) == 0 {
		return CleanupResult{}, nil
	}
	if err = s.persistSnapshotLocked(candidate); err != nil {
		return CleanupResult{}, err
	}
	s.data = candidate
	removed := 0
	for _, id := range ids {
		if err = os.Remove(filepath.Join(s.root, "files", id)); err == nil || errors.Is(err, os.ErrNotExist) {
			removed++
		} else {
			return CleanupResult{Removed: removed}, err
		}
	}
	d, err := os.Open(filepath.Join(s.root, "files"))
	if err == nil {
		err = d.Sync()
		_ = d.Close()
	}
	return CleanupResult{Removed: removed}, err
}
