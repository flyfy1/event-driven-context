// Package localcollection maintains a pull-only local notes and attachment cache.
package localcollection

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
)

const manifestName = "manifest.json"
const maxMetadataBytes = 128 << 10

type NotesCoverage struct {
	Revision        int64 `json:"revision"`
	ThroughSequence int64 `json:"through_sequence"`
	Complete        bool  `json:"complete"`
}
type CatalogCoverage struct {
	ThroughSequence int64  `json:"through_sequence"`
	Cursor          string `json:"cursor,omitempty"`
	Started         bool   `json:"started"`
	Complete        bool   `json:"complete"`
}
type BytesCoverage struct {
	ThroughSequence int64 `json:"through_sequence"`
	Complete        bool  `json:"complete"`
}
type Manifest struct {
	Version   int             `json:"version"`
	Server    string          `json:"server"`
	ProjectID string          `json:"project_id"`
	Notes     NotesCoverage   `json:"notes"`
	Catalog   CatalogCoverage `json:"catalog"`
	Bytes     BytesCoverage   `json:"bytes"`
}
type Collection struct {
	root         *os.Root
	lock         *os.File
	directory    string
	manifest     Manifest
	recordHashes map[string]string
}

type ConflictError struct {
	Path   string
	Reason string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("local collection conflict at %s: %s; existing content preserved", e.Path, e.Reason)
}

func CanonicalOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "443" && u.Scheme == "https" || port == "80" && u.Scheme == "http" {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return strings.ToLower(u.Scheme) + "://" + host
}
func Open(directory, server, project string) (c *Collection, err error) {
	if CanonicalOrigin(server) == "" {
		return nil, fmt.Errorf("valid server origin is required")
	}
	if project == "" {
		return nil, fmt.Errorf("project is required")
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	if st, e := os.Lstat(directory); e == nil && (!st.IsDir() || st.Mode()&os.ModeSymlink != 0) {
		return nil, fmt.Errorf("collection must be a directory, not a symlink")
	} else if e != nil && !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	if err = os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	c = &Collection{root: root, directory: directory, recordHashes: map[string]string{}, manifest: Manifest{Version: 1, Server: CanonicalOrigin(server), ProjectID: project}}
	opened := c
	defer func() {
		if err != nil {
			opened.Close()
		}
	}()
	if err = c.checkPath(".edc-collection.lock", false); err != nil {
		return nil, err
	}
	c.lock, err = root.OpenFile(".edc-collection.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(c.lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, fmt.Errorf("another operation is using this collection")
	}
	var saved Manifest
	exists, err := c.readRecord(manifestName, &saved)
	if err != nil {
		return nil, err
	}
	if exists {
		if saved.Version != 1 || saved.Notes.Revision < 0 || saved.Notes.ThroughSequence < 0 || saved.Catalog.ThroughSequence < 0 || saved.Bytes.ThroughSequence < 0 || saved.Catalog.Complete && !saved.Catalog.Started {
			return nil, fmt.Errorf("invalid collection manifest")
		}
		if saved.Server != c.manifest.Server || saved.ProjectID != project {
			return nil, fmt.Errorf("collection belongs to another server or project; choose a different directory")
		}
		c.manifest = saved
	} else if err = c.saveManifest(); err != nil {
		return nil, err
	}
	return c, nil
}
func (c *Collection) Close() error {
	if c == nil {
		return nil
	}
	if c.lock != nil {
		_ = syscall.Flock(int(c.lock.Fd()), syscall.LOCK_UN)
		_ = c.lock.Close()
		c.lock = nil
	}
	if c.root != nil {
		err := c.root.Close()
		c.root = nil
		return err
	}
	return nil
}
func (c *Collection) Manifest() Manifest { return c.manifest }
func (c *Collection) NotesDirectory() (string, error) {
	if err := c.checkPath("notes", true); err != nil {
		return "", err
	}
	if err := c.root.MkdirAll("notes", 0700); err != nil {
		return "", err
	}
	return filepath.Join(c.directory, "notes"), nil
}
func (c *Collection) BeginNotes() error { c.manifest.Notes.Complete = false; return c.saveManifest() }
func (c *Collection) CompleteNotes(revision, through int64) error {
	if revision < 0 || through < 0 {
		return fmt.Errorf("invalid notes coverage")
	}
	c.manifest.Notes = NotesCoverage{Revision: revision, ThroughSequence: through, Complete: true}
	return c.saveManifest()
}
func (c *Collection) saveManifest() error { return c.writeRecord(manifestName, c.manifest) }

// A record checksum covers the exact payload bytes. Metadata edits are detected
// without a growing inventory or a non-atomic metadata/sidecar pair.
type record struct {
	SHA256 string          `json:"sha256"`
	Data   json.RawMessage `json:"data"`
}

func (c *Collection) readRecord(name string, out any) (bool, error) {
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
	raw, err := io.ReadAll(io.LimitReader(f, maxMetadataBytes+1))
	if err != nil {
		return false, err
	}
	if len(raw) > maxMetadataBytes {
		return false, &ConflictError{name, "metadata exceeds size limit"}
	}
	var r record
	if err = strictJSON(raw, &r); err != nil {
		return false, &ConflictError{name, "invalid managed metadata"}
	}
	if r.SHA256 != digest(r.Data) {
		return false, &ConflictError{name, "metadata was locally edited"}
	}
	if err = strictJSON(r.Data, out); err != nil {
		return false, &ConflictError{name, "unsupported managed metadata"}
	}
	c.recordHashes[name] = digest(raw)
	return true, nil
}
func (c *Collection) writeRecord(name string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(record{SHA256: digest(data), Data: data})
	if err != nil {
		return err
	}
	if len(raw) > maxMetadataBytes {
		return fmt.Errorf("metadata exceeds size limit")
	}
	raw = append(raw, '\n')
	if err = c.checkPath(name, false); err != nil {
		return err
	}
	current, exists, err := c.readRawRecord(name)
	if err != nil {
		return err
	}
	expected := c.recordHashes[name]
	if exists {
		if expected == "" && bytes.Equal(current, raw) {
			c.recordHashes[name] = digest(current)
			return nil
		}
		if expected == "" || digest(current) != expected {
			return &ConflictError{name, "metadata changed during this operation"}
		}
	} else if expected != "" {
		return &ConflictError{name, "metadata was removed during this operation"}
	}
	if err = c.atomicWrite(name, raw); err != nil {
		return err
	}
	c.recordHashes[name] = digest(raw)
	return nil
}
func (c *Collection) readRawRecord(name string) ([]byte, bool, error) {
	f, err := c.root.Open(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxMetadataBytes+1))
	if err != nil {
		return nil, true, err
	}
	if len(raw) > maxMetadataBytes {
		return nil, true, &ConflictError{name, "metadata exceeds size limit"}
	}
	return raw, true, nil
}
func strictJSON(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}
func digest(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }
func (c *Collection) checkPath(name string, directory bool) error {
	if name == "" || path.Clean(name) != name || path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, "\\") {
		return fmt.Errorf("invalid collection path")
	}
	parts := strings.Split(name, "/")
	for i := range parts {
		p := strings.Join(parts[:i+1], "/")
		st, err := c.root.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return &ConflictError{p, "symlinks are not supported"}
		}
		wantDir := i < len(parts)-1 || directory
		if wantDir && !st.IsDir() || !wantDir && !st.Mode().IsRegular() {
			return &ConflictError{p, "unexpected file type"}
		}
	}
	return nil
}
func (c *Collection) atomicWrite(name string, data []byte) error {
	if err := c.checkPath(name, false); err != nil {
		return err
	}
	dir := path.Dir(name)
	if err := c.root.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, temp, err := c.tempFile(dir)
	if err != nil {
		return err
	}
	defer c.root.Remove(temp)
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if e := file.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	if err = c.checkPath(name, false); err != nil {
		return err
	}
	if err = c.root.Rename(temp, name); err != nil {
		return err
	}
	return c.syncDirectory(dir)
}
func (c *Collection) tempFile(dir string) (*os.File, string, error) {
	for i := 0; i < 100; i++ {
		name := path.Join(dir, fmt.Sprintf(".edc-write-%d-%d", os.Getpid(), i))
		f, err := c.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if !errors.Is(err, os.ErrExist) {
			return f, name, err
		}
	}
	return nil, "", fmt.Errorf("cannot allocate collection temporary file")
}
func (c *Collection) syncDirectory(dir string) error {
	f, err := c.root.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
