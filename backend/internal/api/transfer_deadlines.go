package api

import (
	"errors"
	"log/slog"
	"net/http"
	"time"
)

// extendTransferDeadlines budgets a transfer at 64 KiB/s plus three minutes
// for processing. maxBytes must be the route's server-controlled size limit,
// never a client-supplied Content-Length, so even stalled transfers are bounded.
func extendTransferDeadlines(w http.ResponseWriter, maxBytes int64, upload bool) {
	const bytesPerSecond = 64 << 10
	deadline := time.Now().Add(3*time.Minute + time.Duration((maxBytes+bytesPerSecond-1)/bytesPerSecond)*time.Second)
	controller := http.NewResponseController(w)
	if upload {
		if err := controller.SetReadDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
			slog.Debug("could not extend transfer read deadline", "error", err)
		}
		// WriteTimeout starts at the request headers, before the upload is read.
		// Allow the response to be written even when the upload uses its budget.
		deadline = deadline.Add(10 * time.Second)
	}
	if err := controller.SetWriteDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
		slog.Debug("could not extend transfer write deadline", "error", err)
	}
}
