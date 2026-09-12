package capture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"event-driven-context/internal/v2"
	"github.com/google/uuid"
)

// Enqueue durably records user-side events that have not been confirmed by the
// server. Callers must generate IDs before calling it and retain only failed or
// unconfirmed items from a partial batch response.
func (m *Manager) Enqueue(binding Binding, events []v2.EventInput) (int, error) {
	if binding.ProjectID == "" || binding.AccountID == "" || binding.Server == "" {
		return 0, fmt.Errorf("complete binding is required")
	}
	if len(events) < 1 || len(events) > v2.MaxBatchEvents {
		return 0, fmt.Errorf("event batch must contain 1 to %d items", v2.MaxBatchEvents)
	}
	added := 0
	for i, event := range events {
		if _, err := uuid.Parse(event.ID); err != nil {
			return added, fmt.Errorf("event %d id must be a UUID", i+1)
		}
		created, err := m.enqueue(binding, event)
		if err != nil {
			return added, err
		}
		if created {
			added++
		}
	}
	return added, nil
}

func (m *Manager) enqueue(binding Binding, event v2.EventInput) (bool, error) {
	dir := filepath.Join(m.root, "outbox", scopeKey(binding))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, fmt.Errorf("create outbox: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return false, fmt.Errorf("protect outbox: %w", err)
	}
	path := filepath.Join(dir, event.ID+".json")
	item := OutboxItem{Binding: binding, Event: event, QueuedAt: now()}
	b, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		return false, err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(dir, ".pending-*")
	if err != nil {
		return false, fmt.Errorf("create outbox temporary item: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err = tmp.Chmod(0o600); err == nil {
		_, err = tmp.Write(b)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return false, err
	}
	// Link publishes the fully synced inode without overwriting an existing item.
	// This is atomic even when multiple hook processes enqueue the same UUID.
	err = os.Link(tmpName, path)
	if errors.Is(err, os.ErrExist) {
		existing, readErr := readOutboxFile(path)
		if readErr != nil {
			quarantine := path + ".corrupt-" + time.Now().UTC().Format("20060102T150405.000000000Z")
			if renameErr := os.Rename(path, quarantine); renameErr != nil {
				return false, fmt.Errorf("quarantine corrupt outbox item: %w", renameErr)
			}
			if linkErr := os.Link(tmpName, path); linkErr != nil {
				return false, fmt.Errorf("replace corrupt outbox item: %w", linkErr)
			}
			return true, syncDirectory(dir)
		}
		before, _ := json.Marshal(existing.Event)
		after, _ := json.Marshal(event)
		if string(before) != string(after) || existing.Binding != binding {
			return false, fmt.Errorf("outbox event id %s has different content", event.ID)
		}
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("publish outbox item: %w", err)
	}
	return true, syncDirectory(dir)
}

func (m *Manager) OutboxList(binding Binding) ([]OutboxItem, error) {
	items, _, err := m.listOutbox(binding)
	return items, err
}

func (m *Manager) listOutbox(binding Binding) ([]OutboxItem, int, error) {
	dir := filepath.Join(m.root, "outbox", scopeKey(binding))
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []OutboxItem{}, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	items := make([]OutboxItem, 0, len(entries))
	corrupt := 0
	var itemErrors []error
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".pending-") {
			continue
		}
		if strings.Contains(entry.Name(), ".corrupt-") {
			corrupt++
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		item, readErr := readOutboxFile(filepath.Join(dir, entry.Name()))
		if readErr != nil {
			corrupt++
			itemErrors = append(itemErrors, readErr)
			continue
		}
		if item.Binding.Server != binding.Server || item.Binding.AccountID != binding.AccountID || item.Binding.ProjectID != binding.ProjectID {
			corrupt++
			itemErrors = append(itemErrors, fmt.Errorf("outbox scope mismatch in %s", entry.Name()))
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].QueuedAt == items[j].QueuedAt {
			return items[i].Event.ID < items[j].Event.ID
		}
		return items[i].QueuedAt < items[j].QueuedAt
	})
	return items, corrupt, errors.Join(itemErrors...)
}

func (m *Manager) Flush(ctx context.Context, binding Binding) (FlushResult, error) {
	if m.sender == nil {
		return FlushResult{}, fmt.Errorf("capture sender is not configured")
	}
	items, _, err := m.listOutbox(binding)
	result := FlushResult{}
	firstErr := err
	for _, item := range items {
		if err = ctx.Err(); err != nil {
			firstErr = err
			break
		}
		response, sendErr := m.sender.RecordEvents(ctx, binding.ProjectID, v2.RecordEventsInput{Events: []v2.EventInput{item.Event}})
		ok, message := delivered(response, item.Event.ID)
		if sendErr != nil && message == "server omitted event result" {
			_ = m.noteAttempt(item, sendErr.Error())
			if firstErr == nil {
				firstErr = sendErr
			}
			// With no per-item receipt, a transport/auth outage likely applies to
			// every later item in this scope. Preserve ordering and retry later.
			break
		}
		if !ok {
			if sendErr != nil {
				message += ": " + sendErr.Error()
			}
			_ = m.noteAttempt(item, message)
			if firstErr == nil {
				firstErr = fmt.Errorf("deliver %s: %s", item.Event.ID, message)
			}
			continue
		}
		path := filepath.Join(m.root, "outbox", scopeKey(binding), item.Event.ID+".json")
		if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return result, err
		}
		result.Delivered++
	}
	remaining, remainingCorrupt, countErr := m.listOutbox(binding)
	result.Pending = len(remaining) + remainingCorrupt
	if countErr != nil && firstErr == nil {
		firstErr = countErr
	}
	return result, firstErr
}

func (m *Manager) noteAttempt(item OutboxItem, message string) error {
	item.Attempts++
	item.LastError = redactText(message)
	path := filepath.Join(m.root, "outbox", scopeKey(item.Binding), item.Event.ID+".json")
	return writeJSONAtomic(path, item, 0o600)
}

func readOutboxFile(path string) (OutboxItem, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return OutboxItem{}, err
	}
	var item OutboxItem
	decoder := json.NewDecoder(strings.NewReader(string(b)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&item); err != nil || item.Event.ID == "" || item.Binding.ProjectID == "" {
		return OutboxItem{}, fmt.Errorf("invalid outbox item %s", filepath.Base(path))
	}
	return item, nil
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	err = dir.Sync()
	closeErr := dir.Close()
	if err != nil {
		return err
	}
	return closeErr
}
