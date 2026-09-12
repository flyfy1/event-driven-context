package api

import (
	"context"
	"net/http"
	"strconv"

	"event-driven-context/internal/core"
	"event-driven-context/internal/v2"
)

type fileCatalogService interface {
	ListFiles(context.Context, string, v2.FileCatalogInput) (v2.FileCatalogPage, error)
	ListFilesAsPlugin(context.Context, v2.PluginPrincipal, v2.FileCatalogInput) (v2.FileCatalogPage, error)
	FileMetadata(context.Context, string, string, *int64) (v2.FileCatalogEntry, error)
	FileMetadataAsPlugin(context.Context, v2.PluginPrincipal, string, *int64) (v2.FileCatalogEntry, error)
	FileReferences(context.Context, string, string, v2.FileCatalogInput) (v2.FileReferencesPage, error)
	FileReferencesAsPlugin(context.Context, v2.PluginPrincipal, string, v2.FileCatalogInput) (v2.FileReferencesPage, error)
}

func catalogInput(r *http.Request) (v2.FileCatalogInput, error) {
	var in v2.FileCatalogInput
	for k, v := range r.URL.Query() {
		if (k != "limit" && k != "cursor" && k != "through_sequence") || len(v) != 1 {
			return in, v2Invalid("invalid catalog query")
		}
	}
	in.Cursor = r.URL.Query().Get("cursor")
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return in, v2Invalid("invalid limit")
		}
		in.Limit = n
	}
	if raw, ok := r.URL.Query()["through_sequence"]; ok {
		n, err := strconv.ParseInt(raw[0], 10, 64)
		if err != nil {
			return in, v2Invalid("invalid through_sequence")
		}
		in.ThroughSequence = &n
	}
	return in, nil
}
func registerFileCatalogHandlers(mux *http.ServeMux, store *core.Store, service v2.ServiceAPI, config Config) {
	s, ok := service.(fileCatalogService)
	if !ok {
		return
	}
	for _, kind := range []string{"catalog", "metadata", "references"} {
		route := "GET /v1/projects/{project_id}/files/" + kind
		if kind != "catalog" {
			route = "GET /v1/projects/{project_id}/files/{file_id}/" + kind
		}
		mux.Handle(route, v2SubjectAuthenticated(store, service, config, core.ScopeRead, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			in, err := catalogInput(r)
			if err != nil {
				failV2(w, err)
				return
			}
			subject := v2SubjectFrom(r.Context())
			var out any
			switch kind {
			case "catalog":
				if subject.plugin != nil {
					out, err = s.ListFilesAsPlugin(r.Context(), *subject.plugin, in)
				} else {
					out, err = s.ListFiles(r.Context(), r.PathValue("project_id"), in)
				}
			case "metadata":
				if in.Cursor != "" || in.Limit != 0 {
					failV2(w, v2Invalid("metadata does not paginate"))
					return
				}
				if subject.plugin != nil {
					out, err = s.FileMetadataAsPlugin(r.Context(), *subject.plugin, r.PathValue("file_id"), in.ThroughSequence)
				} else {
					out, err = s.FileMetadata(r.Context(), r.PathValue("project_id"), r.PathValue("file_id"), in.ThroughSequence)
				}
			case "references":
				if subject.plugin != nil {
					out, err = s.FileReferencesAsPlugin(r.Context(), *subject.plugin, r.PathValue("file_id"), in)
				} else {
					out, err = s.FileReferences(r.Context(), r.PathValue("project_id"), r.PathValue("file_id"), in)
				}
			}
			v2RespondResult(w, http.StatusOK, out, err)
		})))
	}
}
