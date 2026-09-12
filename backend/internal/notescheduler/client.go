package notescheduler

import (
	"context"
	"event-driven-context/internal/notes"
	"event-driven-context/internal/v2"
	"fmt"
)

type client struct {
	s *v2.Service
	p v2.PluginPrincipal
}

func (c client) check(id string) error {
	if id != c.p.ProjectID {
		return fmt.Errorf("organizer project mismatch")
	}
	return nil
}
func (c client) BeginNotesOrganization(ctx context.Context, id, version string) (notes.OrganizationRun, error) {
	if e := c.check(id); e != nil {
		return notes.OrganizationRun{}, e
	}
	return c.s.BeginNotesOrganization(ctx, c.p, notes.BeginInput{PromptVersion: version})
}
func (c client) PublishNotesOrganization(ctx context.Context, id string, in notes.PublishInput) (notes.PublishResult, error) {
	if e := c.check(id); e != nil {
		return notes.PublishResult{}, e
	}
	return c.s.PublishNotesOrganization(ctx, c.p, in)
}
func (c client) CancelNotesOrganization(ctx context.Context, id, run string) error {
	if e := c.check(id); e != nil {
		return e
	}
	return c.s.CancelNotesOrganization(ctx, c.p, run)
}
func (c client) GetEvent(ctx context.Context, id, event string) (v2.Event, error) {
	if e := c.check(id); e != nil {
		return v2.Event{}, e
	}
	return c.s.GetEventAsPlugin(ctx, c.p, event)
}
func (c client) ListFiles(ctx context.Context, id string, in v2.FileCatalogInput) (v2.FileCatalogPage, error) {
	if e := c.check(id); e != nil {
		return v2.FileCatalogPage{}, e
	}
	return c.s.ListFilesAsPlugin(ctx, c.p, in)
}
func (c client) FileMetadata(ctx context.Context, id, file string, through *int64) (v2.FileCatalogEntry, error) {
	if e := c.check(id); e != nil {
		return v2.FileCatalogEntry{}, e
	}
	return c.s.FileMetadataAsPlugin(ctx, c.p, file, through)
}
func (c client) FileReferences(ctx context.Context, id, file string, in v2.FileCatalogInput) (v2.FileReferencesPage, error) {
	if e := c.check(id); e != nil {
		return v2.FileReferencesPage{}, e
	}
	return c.s.FileReferencesAsPlugin(ctx, c.p, file, in)
}
