package capture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"event-driven-context/internal/v2"
)

type Manager struct {
	root   string
	sender Sender
	mu     sync.Mutex
}

func Open(configRoot string, sender Sender) (*Manager, error) {
	if strings.TrimSpace(configRoot) == "" {
		return nil, fmt.Errorf("capture config root is required")
	}
	abs, err := filepath.Abs(configRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve capture config root: %w", err)
	}
	if err = os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("create capture config root: %w", err)
	}
	if err = os.Chmod(abs, 0o700); err != nil {
		return nil, fmt.Errorf("protect capture config root: %w", err)
	}
	return &Manager{root: abs, sender: sender}, nil
}

func (m *Manager) Link(_ context.Context, cwd, projectID, server, accountID string) (Binding, error) {
	directory, err := canonicalDirectory(cwd)
	if err != nil {
		return Binding{}, err
	}
	projectID, accountID = strings.TrimSpace(projectID), strings.TrimSpace(accountID)
	if projectID == "" || accountID == "" {
		return Binding{}, fmt.Errorf("project and authenticated account are required")
	}
	server, err = normalizeServer(server)
	if err != nil {
		return Binding{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	file, err := m.readBindingsLocked()
	if err != nil {
		return Binding{}, err
	}
	binding := Binding{Directory: directory, ProjectID: projectID, Server: server, AccountID: accountID, LinkedAt: now()}
	replaced := false
	for i := range file.Bindings {
		old := file.Bindings[i]
		if old.Directory == directory && old.Server == server && old.AccountID == accountID {
			file.Bindings[i] = binding
			replaced = true
			break
		}
	}
	if !replaced {
		file.Bindings = append(file.Bindings, binding)
	}
	sort.Slice(file.Bindings, func(i, j int) bool {
		a, b := file.Bindings[i], file.Bindings[j]
		return a.Directory+"\x00"+a.Server+"\x00"+a.AccountID < b.Directory+"\x00"+b.Server+"\x00"+b.AccountID
	})
	if err = writeJSONAtomic(filepath.Join(m.root, "bindings.json"), file, 0o600); err != nil {
		return Binding{}, err
	}
	return binding, nil
}

func (m *Manager) CurrentLink(cwd, server, accountID string) (Binding, error) {
	directory, err := canonicalDirectory(cwd)
	if err != nil {
		return Binding{}, err
	}
	server, err = normalizeServer(server)
	if err != nil {
		return Binding{}, err
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return Binding{}, fmt.Errorf("authenticated account is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	file, err := m.readBindingsLocked()
	if err != nil {
		return Binding{}, err
	}
	var matches []Binding
	for _, binding := range file.Bindings {
		if binding.Server == server && binding.AccountID == accountID && within(directory, binding.Directory) {
			matches = append(matches, binding)
		}
	}
	if len(matches) == 0 {
		return Binding{}, fmt.Errorf("directory is not linked for this server and account")
	}
	sort.Slice(matches, func(i, j int) bool { return len(matches[i].Directory) > len(matches[j].Directory) })
	return matches[0], nil
}

func (m *Manager) Status(cwd, server, accountID string) (Status, error) {
	binding, err := m.CurrentLink(cwd, server, accountID)
	if err != nil {
		return Status{}, err
	}
	items, err := m.OutboxList(binding)
	if err != nil {
		return Status{}, err
	}
	return Status{Binding: binding, HooksEnabled: m.captureEnabled(binding, ClaudeCode), OutboxPending: len(items)}, nil
}

func (m *Manager) readBindingsLocked() (bindingsFile, error) {
	path := filepath.Join(m.root, "bindings.json")
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return bindingsFile{Version: 1, Bindings: []Binding{}}, nil
	}
	if err != nil {
		return bindingsFile{}, fmt.Errorf("read bindings: %w", err)
	}
	var out bindingsFile
	if err = json.Unmarshal(b, &out); err != nil || out.Version != 1 {
		return bindingsFile{}, fmt.Errorf("read bindings: unsupported or invalid file")
	}
	return out, nil
}

func canonicalDirectory(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve directory: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve directory links: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("linked path must be an existing directory")
	}
	return filepath.Clean(resolved), nil
}

func normalizeServer(raw string) (string, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("server must be an HTTP(S) origin")
	}
	loopback := u.Hostname() == "localhost"
	if ip := net.ParseIP(u.Hostname()); ip != nil && ip.IsLoopback() {
		loopback = true
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return "", fmt.Errorf("remote capture server must use HTTPS")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func within(path, parent string) bool {
	rel, err := filepath.Rel(parent, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func scopeKey(binding Binding) string {
	sum := sha256.Sum256([]byte(binding.Server + "\x00" + binding.AccountID + "\x00" + binding.ProjectID))
	return hex.EncodeToString(sum[:16])
}

func writeJSONAtomic(path string, value any, mode os.FileMode) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return writeFileAtomic(path, b, mode)
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refuse to replace symlink %s", path)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".capture-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err = tmp.Chmod(mode); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmpName, path)
	}
	if err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	err = dir.Sync()
	closeErr = dir.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func delivered(result v2.RecordEventsResult, id string) (bool, string) {
	for _, item := range result.Results {
		if item.ID != id {
			continue
		}
		if item.Status == "created" || item.Status == "duplicate" {
			return true, ""
		}
		if item.Error != nil {
			return false, item.Error.Code + ": " + item.Error.Message
		}
		return false, "event was not accepted: " + item.Status
	}
	return false, "server omitted event result"
}
