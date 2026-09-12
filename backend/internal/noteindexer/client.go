package noteindexer

import (
	"context"
	"event-driven-context/internal/notes"
	"event-driven-context/internal/v2"
)

// Client is restricted to one organizer's evidence and validated publication.
type Client interface {
	BeginNotesOrganization(context.Context, string, string) (notes.OrganizationRun, error)
	PublishNotesOrganization(context.Context, string, notes.PublishInput) (notes.PublishResult, error)
	CancelNotesOrganization(context.Context, string, string) error
	GetEvent(context.Context, string, string) (v2.Event, error)
	ListFiles(context.Context, string, v2.FileCatalogInput) (v2.FileCatalogPage, error)
	FileMetadata(context.Context, string, string, *int64) (v2.FileCatalogEntry, error)
	FileReferences(context.Context, string, string, v2.FileCatalogInput) (v2.FileReferencesPage, error)
}
