package api

import (
	"context"
	"net/http"

	"event-driven-context/internal/core"
	"event-driven-context/internal/v2"
)

type adminProject struct {
	core.AdminProject
	Stats v2.AdminProjectStats `json:"stats"`
}

type adminTotals struct {
	Users             int   `json:"users"`
	Projects          int   `json:"projects"`
	SharedMemberships int   `json:"shared_memberships"`
	Events            int   `json:"events"`
	Files             int   `json:"files"`
	FileBytes         int64 `json:"file_bytes"`
	States            int   `json:"states"`
	Plugins           int   `json:"plugins"`
}

type adminOverview struct {
	Users    []core.AdminUser `json:"users"`
	Projects []adminProject   `json:"projects"`
	Plugins  []v2.AdminPlugin `json:"plugins"`
	Totals   adminTotals      `json:"totals"`
}

func RegisterAdminHandlers(mux *http.ServeMux, store *core.Store, service v2.ServiceAPI, config Config) {
	admin := func(next http.Handler) http.Handler { return adminSessionAuthenticated(store, config.AdminUsers, next) }
	mux.Handle("GET /v1/admin/overview", admin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, err := store.AdminIdentitySnapshot(r.Context(), config.AdminUsers)
		if err != nil {
			failV2(w, err)
			return
		}
		projectIDs := make([]string, len(identity.Projects))
		for index := range identity.Projects {
			projectIDs[index] = identity.Projects[index].ID
		}
		stats := service.AdminProjectStats(projectIDs)
		statsByProject := make(map[string]v2.AdminProjectStats, len(stats))
		for _, item := range stats {
			statsByProject[item.ProjectID] = item
		}

		overview := adminOverview{
			Users: identity.Users, Projects: make([]adminProject, 0, len(identity.Projects)), Plugins: []v2.AdminPlugin{},
			Totals: adminTotals{Users: len(identity.Users), Projects: len(identity.Projects)},
		}
		for _, project := range identity.Projects {
			projectStats := statsByProject[project.ID]
			overview.Projects = append(overview.Projects, adminProject{AdminProject: project, Stats: projectStats})
			overview.Plugins = append(overview.Plugins, projectStats.Plugins...)
			overview.Totals.SharedMemberships += max(0, len(project.Members)-1)
			overview.Totals.Events += projectStats.EventCount
			overview.Totals.Files += projectStats.FileCount
			overview.Totals.FileBytes += projectStats.FileBytes
			overview.Totals.States += projectStats.StateCount
			overview.Totals.Plugins += projectStats.PluginCount
		}
		respond(w, http.StatusOK, overview)
	})))

	mux.Handle("PATCH /v1/admin/projects/{project_id}/members/{user_id}", admin(jsonEndpointV2(http.StatusOK, func(ctx context.Context, in struct {
		Access string `json:"access"`
	}) (core.AdminProjectMember, error) {
		if in.Access != "member" && in.Access != "none" {
			return core.AdminProjectMember{}, core.Invalid("access must be member or none")
		}
		return store.SetAdminProjectMember(ctx, config.AdminUsers, v2ProjectID(ctx), v2AdminUserID(ctx), in.Access == "member")
	})))
}

// Admin calls accept only the product's first-party browser session cookie.
// OAuth access tokens intentionally cannot inherit site-wide administrator
// privileges even when they belong to an allowlisted user.
func adminSessionAuthenticated(store *core.Store, allowed []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var token string
		for _, name := range []string{sessionCookieName, localSessionCookieName} {
			if cookie, err := r.Cookie(name); err == nil && cookie.Value != "" {
				token = cookie.Value
				break
			}
		}
		userID, _, err := store.Authenticate(r.Context(), token)
		if err != nil {
			w.Header().Set("WWW-Authenticate", "Cookie")
			failV2(w, err)
			return
		}
		ctx := core.WithUser(r.Context(), userID)
		if err = store.RequireAdmin(ctx, allowed); err != nil {
			failV2(w, err)
			return
		}
		ctx = v2PathContext(ctx, r)
		ctx = context.WithValue(ctx, v2AdminUserKey{}, r.PathValue("user_id"))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type v2AdminUserKey struct{}

func v2AdminUserID(ctx context.Context) string {
	value, _ := ctx.Value(v2AdminUserKey{}).(string)
	return value
}
