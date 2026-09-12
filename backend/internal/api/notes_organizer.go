package api

import (
	"context"
	"net/http"

	"event-driven-context/internal/core"
	"event-driven-context/internal/notes"
	"event-driven-context/internal/v2"
)

type organizerService interface {
	BeginNotesOrganization(context.Context, v2.PluginPrincipal, notes.BeginInput) (notes.OrganizationRun, error)
	PublishNotesOrganization(context.Context, v2.PluginPrincipal, notes.PublishInput) (notes.PublishResult, error)
	CancelNotesOrganization(context.Context, v2.PluginPrincipal, string) error
}

func registerNotesOrganizerHandlers(mux *http.ServeMux, store *core.Store, service v2.ServiceAPI, config Config) {
	s, ok := service.(organizerService)
	if !ok {
		return
	}
	wrap := func(h http.Handler) http.Handler {
		return v2SubjectAuthenticated(store, service, config, core.ScopeWrite, h)
	}
	mux.Handle("POST /v1/projects/{project_id}/notes/organizer/begin", wrap(jsonEndpointV2(200, func(ctx context.Context, in notes.BeginInput) (notes.OrganizationRun, error) {
		p := v2SubjectFrom(ctx).plugin
		if p == nil {
			return notes.OrganizationRun{}, &v2.Error{Code: "forbidden", Message: "organizer plugin credential required"}
		}
		return s.BeginNotesOrganization(ctx, *p, in)
	})))
	mux.Handle("POST /v1/projects/{project_id}/notes/organizer/publish", wrap(jsonEndpointV2(200, func(ctx context.Context, in notes.PublishInput) (notes.PublishResult, error) {
		p := v2SubjectFrom(ctx).plugin
		if p == nil {
			return notes.PublishResult{}, &v2.Error{Code: "forbidden", Message: "organizer plugin credential required"}
		}
		return s.PublishNotesOrganization(ctx, *p, in)
	})))
	mux.Handle("POST /v1/projects/{project_id}/notes/organizer/cancel", wrap(jsonEndpointV2(200, func(ctx context.Context, in struct {
		RunID string `json:"run_id"`
	}) (core.Empty, error) {
		p := v2SubjectFrom(ctx).plugin
		if p == nil {
			return core.Empty{}, &v2.Error{Code: "forbidden", Message: "organizer plugin credential required"}
		}
		return core.Empty{}, s.CancelNotesOrganization(ctx, *p, in.RunID)
	})))
}
