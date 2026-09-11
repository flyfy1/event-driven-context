package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"event-driven-context/internal/core"
	"event-driven-context/internal/v2"
)

const v2MultipartOverhead = 1 << 20

type v2Subject struct {
	userID string
	plugin *v2.PluginPrincipal
}

type v2SubjectKey struct{}

func v2SubjectFrom(ctx context.Context) v2Subject {
	subject, _ := ctx.Value(v2SubjectKey{}).(v2Subject)
	return subject
}

// RegisterV2Handlers registers the project-scoped V2 JSON and file endpoints.
// Authentication endpoints and OAuth discovery are registered by the outer
// handler because they continue to use the identity database in core.Store.
func RegisterV2Handlers(mux *http.ServeMux, store *core.Store, service v2.ServiceAPI, config Config) {
	readUser := func(next http.Handler) http.Handler { return v2UserAuthenticated(store, config, core.ScopeRead, next) }
	writeUser := func(next http.Handler) http.Handler { return v2UserAuthenticated(store, config, core.ScopeWrite, next) }
	readSubject := func(next http.Handler) http.Handler {
		return v2SubjectAuthenticated(store, service, config, core.ScopeRead, next)
	}
	writeSubject := func(next http.Handler) http.Handler {
		return v2SubjectAuthenticated(store, service, config, core.ScopeWrite, next)
	}

	mux.Handle("GET /v1/projects", readUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out, err := store.ListProjects(r.Context(), core.Empty{})
		v2RespondResult(w, http.StatusOK, out, err)
	})))
	mux.Handle("POST /v1/projects", writeUser(jsonEndpointV2(http.StatusCreated, store.CreateProject)))
	mux.Handle("GET /v1/projects/{project_id}/members", readUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out, err := store.ListMembers(r.Context(), core.ProjectRef{ProjectID: r.PathValue("project_id")})
		v2RespondResult(w, http.StatusOK, out, err)
	})))
	mux.Handle("POST /v1/projects/{project_id}/members", writeUser(jsonEndpointV2(http.StatusOK, func(ctx context.Context, in struct {
		Username string `json:"username"`
	}) (core.User, error) {
		return store.AddMember(ctx, core.MemberInput{ProjectID: v2ProjectID(ctx), Username: in.Username})
	})))

	mux.Handle("POST /v1/projects/{project_id}/events", writeSubject(jsonEndpointV2(http.StatusOK, func(ctx context.Context, in v2.RecordEventsInput) (v2.RecordEventsResult, error) {
		subject := v2SubjectFrom(ctx)
		if subject.plugin != nil {
			return service.RecordEventsAsPlugin(ctx, *subject.plugin, in)
		}
		return service.RecordEvents(ctx, v2ProjectID(ctx), in)
	})))
	mux.Handle("POST /v1/projects/{project_id}/events/query", readSubject(jsonEndpointV2(http.StatusOK, func(ctx context.Context, in v2.QueryEventsInput) (v2.EventsPage, error) {
		subject := v2SubjectFrom(ctx)
		if subject.plugin != nil {
			return service.QueryEventsAsPlugin(ctx, *subject.plugin, in)
		}
		return service.QueryEvents(ctx, v2ProjectID(ctx), in)
	})))
	mux.Handle("GET /v1/projects/{project_id}/events/{event_id}", readSubject(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		subject := v2SubjectFrom(r.Context())
		var out v2.Event
		var err error
		if subject.plugin != nil {
			out, err = service.GetEventAsPlugin(r.Context(), *subject.plugin, r.PathValue("event_id"))
		} else {
			out, err = service.GetEvent(r.Context(), r.PathValue("project_id"), r.PathValue("event_id"))
		}
		v2RespondResult(w, http.StatusOK, out, err)
	})))
	mux.Handle("GET /v1/projects/{project_id}/metadata", readSubject(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, err := v2SingleQuery(r, "key")
		if err != nil {
			failV2(w, err)
			return
		}
		input := v2.MetadataInput{Key: key}
		subject := v2SubjectFrom(r.Context())
		var out v2.MetadataResult
		var callErr error
		if subject.plugin != nil {
			out, callErr = service.ListMetadataAsPlugin(r.Context(), *subject.plugin, input)
		} else {
			out, callErr = service.ListMetadata(r.Context(), r.PathValue("project_id"), input)
		}
		v2RespondResult(w, http.StatusOK, out, callErr)
	})))

	mux.Handle("POST /v1/projects/{project_id}/files", writeUser(v2FileUploadEndpoint(service)))
	mux.Handle("GET /v1/projects/{project_id}/files/{file_id}", readSubject(v2FileDownloadEndpoint(service)))

	mux.Handle("GET /v1/projects/{project_id}/state", readSubject(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prefix, err := v2SingleQuery(r, "prefix")
		if err != nil {
			failV2(w, err)
			return
		}
		subject := v2SubjectFrom(r.Context())
		in := v2.ListStateInput{Prefix: prefix}
		var out v2.StatesResult
		if subject.plugin != nil {
			out, err = service.ListStateAsPlugin(r.Context(), *subject.plugin, in)
		} else {
			out, err = service.ListState(r.Context(), r.PathValue("project_id"), in)
		}
		v2RespondResult(w, http.StatusOK, out, err)
	})))
	mux.Handle("GET /v1/projects/{project_id}/state/{plugin_id}/{name}", readSubject(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		versionRaw, err := v2SingleQuery(r, "version")
		if err != nil {
			failV2(w, err)
			return
		}
		var version *int64
		if versionRaw != "" {
			value, parseErr := strconv.ParseInt(versionRaw, 10, 64)
			if parseErr != nil || value < 1 {
				failV2(w, v2Invalid("version must be a positive integer"))
				return
			}
			version = &value
		}
		in := v2.GetStateInput{Keys: []string{v2StateKey(r)}, Version: version}
		subject := v2SubjectFrom(r.Context())
		var out v2.StatesResult
		if subject.plugin != nil {
			out, err = service.GetStateAsPlugin(r.Context(), *subject.plugin, in)
		} else {
			out, err = service.GetState(r.Context(), r.PathValue("project_id"), in)
		}
		if err != nil {
			failV2(w, err)
			return
		}
		if len(out.States) != 1 {
			failV2(w, &v2.Error{Code: "not_found", Message: "state not found"})
			return
		}
		respond(w, http.StatusOK, out.States[0])
	})))
	mux.Handle("PUT /v1/projects/{project_id}/state/{plugin_id}/{name}", writeSubject(jsonEndpointV2(http.StatusOK, func(ctx context.Context, in v2.PutStateInput) (v2.State, error) {
		key := v2StateKeyFromContext(ctx)
		if in.Key != "" && in.Key != key {
			return v2.State{}, v2Invalid("state key in body must match request path")
		}
		in.Key = key
		subject := v2SubjectFrom(ctx)
		if subject.plugin != nil {
			return service.PutStateAsPlugin(ctx, *subject.plugin, in)
		}
		if in.AsPluginID == "" {
			in.AsPluginID = v2PathPluginID(ctx)
		} else if in.AsPluginID != v2PathPluginID(ctx) {
			return v2.State{}, v2Invalid("as_plugin_id must match request path")
		}
		return service.PutState(ctx, v2ProjectID(ctx), in)
	})))

	mux.Handle("GET /v1/projects/{project_id}/plugins", readUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plugins, err := service.ListPlugins(r.Context(), r.PathValue("project_id"))
		v2RespondResult(w, http.StatusOK, struct {
			Plugins []v2.Installation `json:"plugins"`
		}{Plugins: plugins}, err)
	})))
	mux.Handle("POST /v1/projects/{project_id}/plugins", writeUser(jsonEndpointV2(http.StatusCreated, func(ctx context.Context, in v2.InstallPluginInput) (v2.InstallPluginResult, error) {
		return service.InstallPlugin(ctx, v2ProjectID(ctx), in)
	})))
	mux.Handle("PATCH /v1/projects/{project_id}/plugins/{plugin_id}", writeUser(jsonEndpointV2(http.StatusOK, func(ctx context.Context, in v2PluginPatch) (v2.Installation, error) {
		switch in.Action {
		case "config":
			if in.ExpectedRevision == nil || len(in.Config) == 0 {
				return v2.Installation{}, v2Invalid("config action requires expected_revision and config")
			}
			return service.RevisePlugin(ctx, v2ProjectID(ctx), v2PathPluginID(ctx), v2.RevisePluginInput{ExpectedRevision: *in.ExpectedRevision, Config: in.Config})
		case "pause", "resume":
			if in.ExpectedRevision != nil || len(in.Config) != 0 {
				return v2.Installation{}, v2Invalid("pause and resume actions do not accept config or expected_revision")
			}
			status := "paused"
			if in.Action == "resume" {
				status = "active"
			}
			return service.SetPluginStatus(ctx, v2ProjectID(ctx), v2PathPluginID(ctx), status)
		default:
			return v2.Installation{}, v2Invalid("action must be config, pause, or resume")
		}
	})))
	mux.Handle("POST /v1/projects/{project_id}/plugins/{plugin_id}/runs", writeUser(jsonEndpointV2(http.StatusAccepted, func(ctx context.Context, in v2.ManualRunInput) (v2.ManualRunRequest, error) {
		return service.RequestManualRun(ctx, v2ProjectID(ctx), v2PathPluginID(ctx), in)
	})))
	mux.Handle("DELETE /v1/projects/{project_id}/plugins/{plugin_id}", writeUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out, err := service.RemovePlugin(r.Context(), r.PathValue("project_id"), r.PathValue("plugin_id"))
		v2RespondResult(w, http.StatusOK, out, err)
	})))
}

type v2PluginPatch struct {
	Action           string          `json:"action"`
	ExpectedRevision *int64          `json:"expected_revision,omitempty"`
	Config           json.RawMessage `json:"config,omitempty"`
}

func v2UserAuthenticated(store *core.Store, config Config, requiredScope string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, err := v2AuthenticateUser(r, store, config, requiredScope)
		if err != nil {
			w.Header().Set("WWW-Authenticate", "Bearer")
			failV2(w, err)
			return
		}
		ctx := context.WithValue(core.WithUser(r.Context(), userID), v2SubjectKey{}, v2Subject{userID: userID})
		next.ServeHTTP(w, r.WithContext(v2PathContext(ctx, r)))
	})
}

func v2SubjectAuthenticated(store *core.Store, service v2.ServiceAPI, config Config, requiredScope string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearer(r)
		if userID, err := v2AuthenticateUser(r, store, config, requiredScope); err == nil {
			ctx := context.WithValue(core.WithUser(r.Context(), userID), v2SubjectKey{}, v2Subject{userID: userID})
			next.ServeHTTP(w, r.WithContext(v2PathContext(ctx, r)))
			return
		} else if v2ErrorCode(err) == "forbidden" {
			failV2(w, err)
			return
		}
		principal, err := service.AuthenticatePlugin(token)
		if err != nil {
			w.Header().Set("WWW-Authenticate", "Bearer")
			failV2(w, err)
			return
		}
		if principal.ProjectID != r.PathValue("project_id") {
			failV2(w, core.ErrNotFound)
			return
		}
		ctx := context.WithValue(r.Context(), v2SubjectKey{}, v2Subject{plugin: &principal})
		next.ServeHTTP(w, r.WithContext(v2PathContext(ctx, r)))
	})
}

func v2AuthenticateUser(r *http.Request, store *core.Store, config Config, requiredScope string) (string, error) {
	token := bearer(r)
	if strings.HasPrefix(token, "edco_") {
		if config.PublicBaseURL == "" {
			return "", core.ErrUnauthenticated
		}
		info, err := store.AuthenticateOAuth(r.Context(), token, strings.TrimRight(config.PublicBaseURL, "/")+"/mcp")
		if err != nil {
			return "", err
		}
		if !slices.Contains(info.Scopes, requiredScope) {
			return "", &core.Error{Code: "forbidden", Message: "OAuth token missing required scope " + requiredScope}
		}
		return info.UserID, nil
	}
	userID, _, err := store.Authenticate(r.Context(), token)
	return userID, err
}

func v2PathContext(ctx context.Context, r *http.Request) context.Context {
	ctx = context.WithValue(ctx, v2ProjectKey{}, r.PathValue("project_id"))
	ctx = context.WithValue(ctx, v2PluginKey{}, r.PathValue("plugin_id"))
	ctx = context.WithValue(ctx, v2StateNameKey{}, r.PathValue("name"))
	return ctx
}

type v2ProjectKey struct{}
type v2PluginKey struct{}
type v2StateNameKey struct{}

func v2ProjectID(ctx context.Context) string {
	value, _ := ctx.Value(v2ProjectKey{}).(string)
	return value
}
func v2PathPluginID(ctx context.Context) string {
	value, _ := ctx.Value(v2PluginKey{}).(string)
	return value
}
func v2StateKeyFromContext(ctx context.Context) string {
	name, _ := ctx.Value(v2StateNameKey{}).(string)
	return v2PathPluginID(ctx) + "/" + name
}
func v2StateKey(r *http.Request) string { return r.PathValue("plugin_id") + "/" + r.PathValue("name") }

func v2FileUploadEndpoint(service v2.ServiceAPI) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
			failV2(w, v2Invalid("Content-Type must be multipart/form-data with boundary"))
			return
		}
		if err = r.ParseMultipartForm(v2.MaxFileBytes); err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) || errors.Is(err, multipart.ErrMessageTooLarge) {
				failV2(w, &v2.Error{Code: "too_large", Message: "file max 50 MiB"})
			} else {
				failV2(w, v2Invalid("invalid multipart body"))
			}
			return
		}
		defer func() {
			if cleanupErr := r.MultipartForm.RemoveAll(); cleanupErr != nil {
				slog.Debug("could not remove V2 multipart temporary files", "error", cleanupErr)
			}
		}()
		if len(r.MultipartForm.Value) > 1 || len(r.MultipartForm.Value["sha256"]) > 1 {
			failV2(w, v2Invalid("only one optional sha256 field is allowed"))
			return
		}
		for key := range r.MultipartForm.Value {
			if key != "sha256" {
				failV2(w, v2Invalid("unexpected multipart field %q", key))
				return
			}
		}
		if len(r.MultipartForm.File) != 1 || len(r.MultipartForm.File["file"]) != 1 {
			failV2(w, v2Invalid("exactly one file field is required"))
			return
		}
		for key := range r.MultipartForm.File {
			if key != "file" {
				failV2(w, v2Invalid("unexpected multipart file field %q", key))
				return
			}
		}
		header := r.MultipartForm.File["file"][0]
		if header.Size > v2.MaxFileBytes {
			failV2(w, &v2.Error{Code: "too_large", Message: "file max 50 MiB"})
			return
		}
		file, err := header.Open()
		if err != nil {
			failV2(w, v2Invalid("could not read file"))
			return
		}
		defer file.Close()
		out, err := service.PutFile(r.Context(), r.PathValue("project_id"), v2.FileUpload{
			Filename: header.Filename, MediaType: header.Header.Get("Content-Type"), SHA256: firstValue(r.MultipartForm.Value["sha256"]), SizeBytes: header.Size, Reader: io.LimitReader(file, v2.MaxFileBytes+1),
		})
		v2RespondResult(w, http.StatusCreated, out, err)
	})
}

func v2FileDownloadEndpoint(service v2.ServiceAPI) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		subject := v2SubjectFrom(r.Context())
		var info v2.FileInfo
		var file io.ReadCloser
		var err error
		if subject.plugin != nil {
			info, file, err = service.OpenFileAsPlugin(r.Context(), *subject.plugin, r.PathValue("file_id"))
		} else {
			info, file, err = service.OpenFile(r.Context(), r.PathValue("project_id"), r.PathValue("file_id"))
		}
		if err != nil {
			failV2(w, err)
			return
		}
		defer file.Close()
		w.Header().Set("Content-Type", info.MediaType)
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": info.Filename}))
		w.Header().Set("Content-Length", strconv.FormatInt(info.SizeBytes, 10))
		w.Header().Set("X-EDC-SHA256", info.SHA256)
		w.Header().Set("X-EDC-File-ID", info.ID)
		w.WriteHeader(http.StatusOK)
		if _, err = io.Copy(w, file); err != nil {
			slog.Debug("V2 raw file response write failed", "file_id", info.ID, "error", err)
		}
	})
}

func v2SingleQuery(r *http.Request, allowed string) (string, error) {
	for key, values := range r.URL.Query() {
		if key != allowed || len(values) != 1 {
			return "", v2Invalid("unexpected or repeated query parameter %q", key)
		}
	}
	return r.URL.Query().Get(allowed), nil
}

func firstValue(values []string) string {
	if len(values) == 1 {
		return values[0]
	}
	return ""
}

func jsonEndpointV2[I, O any](status int, fn func(context.Context, I) (O, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in I
		if err := decodeV2(r, &in); err != nil {
			failV2(w, err)
			return
		}
		out, err := fn(r.Context(), in)
		v2RespondResult(w, status, out, err)
	})
}

func decodeV2(r *http.Request, out any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return v2Invalid("Content-Type must be application/json")
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return &v2.Error{Code: "too_large", Message: "request max 2 MiB"}
		}
		return v2Invalid("could not read request")
	}
	if !utf8.Valid(data) || len(strings.TrimSpace(string(data))) == 0 {
		return v2Invalid("body must be a UTF-8 JSON object")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(out); err != nil {
		return v2Invalid("invalid JSON: %s", err)
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return v2Invalid("expected exactly one JSON object")
	}
	return nil
}

func v2RespondResult[T any](w http.ResponseWriter, status int, out T, err error) {
	if err != nil {
		failV2(w, err)
		return
	}
	respond(w, status, out)
}

func failV2(w http.ResponseWriter, err error) {
	var appErr *v2.Error
	if !errors.As(err, &appErr) {
		var coreErr *core.Error
		if errors.As(err, &coreErr) {
			appErr = &v2.Error{Code: coreErr.Code, Message: coreErr.Message}
		} else {
			slog.Error("V2 API operation failed", "error", err)
			appErr = &v2.Error{Code: "internal", Message: "internal server error"}
		}
	}
	status := http.StatusInternalServerError
	switch appErr.Code {
	case "invalid_input", "invalid_ref":
		status = http.StatusBadRequest
	case "unauthenticated":
		status = http.StatusUnauthorized
	case "forbidden", "forbidden_namespace", "plugin_paused":
		status = http.StatusForbidden
	case "not_found":
		status = http.StatusNotFound
	case "conflict", "state_version_mismatch":
		status = http.StatusConflict
	case "too_large":
		status = http.StatusRequestEntityTooLarge
	case "unsupported_media_type":
		status = http.StatusUnsupportedMediaType
	case "rate_limited":
		status = http.StatusTooManyRequests
	}
	respond(w, status, map[string]any{"error": appErr})
}

func v2Invalid(format string, args ...any) error {
	return &v2.Error{Code: "invalid_input", Message: fmt.Sprintf(format, args...)}
}

func v2ErrorCode(err error) string {
	var next *v2.Error
	if errors.As(err, &next) {
		return next.Code
	}
	var legacy *core.Error
	if errors.As(err, &legacy) {
		return legacy.Code
	}
	return ""
}
