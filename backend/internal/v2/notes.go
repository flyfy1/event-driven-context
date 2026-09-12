package v2

import (
	"context"
	"errors"
	"path/filepath"

	"event-driven-context/internal/core"
	"event-driven-context/internal/notes"
)

// Notes use the existing membership boundary and the V2 service writer lock.
// Organizer writes require their separate plugin permission and lease.
func (s *Service) ExportNotes(ctx context.Context, projectID string) (notes.Export, error) {
	if err := s.identity.RequireProjectMember(ctx, projectID); err != nil {
		return notes.Export{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	store, err := s.noteStore(projectID)
	if err != nil {
		return notes.Export{}, err
	}
	out, err := store.Export()
	return out, notesError(err)
}

func (s *Service) SyncNotes(ctx context.Context, projectID string, in notes.SyncInput) (notes.Export, error) {
	if err := s.identity.RequireProjectMember(ctx, projectID); err != nil {
		return notes.Export{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	store, err := s.noteStore(projectID)
	if err != nil {
		return notes.Export{}, err
	}
	out, err := store.Sync(in, core.UserID(ctx))
	return out, notesError(err)
}

func (s *Service) noteStore(projectID string) (notes.Store, error) {
	root, err := filepath.EvalSymlinks(s.root)
	return notes.Store{Root: filepath.Join(root, "projects", projectID), ProjectID: projectID}, err
}

func notesError(err error) error {
	var noteErr *notes.Error
	if errors.As(err, &noteErr) {
		return &Error{Code: noteErr.Code, Message: noteErr.Message}
	}
	return err
}
