package api

import (
	"context"
	"net/http"

	"event-driven-context/internal/core"
	"event-driven-context/internal/notes"
	"event-driven-context/internal/v2"
)

// Keep the optional notes surface separate from the existing conversation MCP
// contract until the complete notes read/write tool set is introduced.
type notesService interface {
	ExportNotes(context.Context, string) (notes.Export, error)
}

func registerNotesHandlers(mux *http.ServeMux, store *core.Store, service v2.ServiceAPI, config Config) {
	noteService, ok := service.(notesService)
	if !ok {
		return
	}
	mux.Handle("GET /v1/projects/{project_id}/notes/export", v2UserAuthenticated(store, config, core.ScopeRead, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out, err := noteService.ExportNotes(r.Context(), r.PathValue("project_id"))
		v2RespondResult(w, http.StatusOK, out, err)
	})))
}
