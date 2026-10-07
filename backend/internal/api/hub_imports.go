package api

import (
	"encoding/base64"
	"net/http"
	"os"

	"event-driven-context/internal/core"
)

func registerHubImportHandlers(mux *http.ServeMux, store *core.Store) {
	key, err := base64.StdEncoding.DecodeString(os.Getenv("EDC_HUB_CREDENTIAL_KEY"))
	if err != nil {
		key = nil
	}
	registerHubImportWithKey(mux, store, key)
}
func registerHubImportWithKey(mux *http.ServeMux, store *core.Store, key []byte) {
	mux.Handle("POST /v1/hub/imports", authenticated(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Allow bounded base64 ZIP uploads and escaped UTF-8 text snapshots.
		r.Body = http.MaxBytesReader(w, r.Body, core.HubImportRequestMaxBytes)
		var in core.HubImportInput
		if err := decode(r, &in); err != nil {
			hubFail(w, err)
			return
		}
		out, err := store.ImportHubSnapshot(r.Context(), in, key)
		w.Header().Set("Cache-Control", "no-store")
		v2RespondResult(w, http.StatusCreated, out, err)
	})))
}
