package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"event-driven-context/internal/api"
	"event-driven-context/internal/core"
	"event-driven-context/internal/notescheduler"
	"event-driven-context/internal/transcription"
	"event-driven-context/internal/v2"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
func run() (runErr error) {
	addr := flag.String("addr", "127.0.0.1:8080", "HTTP listen address")
	dbPath := flag.String("db", "data/context.db", "SQLite database path")
	dataDir := flag.String("data", "data", "directory for immutable event and uploaded-file data")
	origins := flag.String("allowed-origins", "", "comma-separated browser origins; empty rejects all Origin-bearing requests")
	publicBaseURL := flag.String("public-base-url", "", "public HTTPS origin used for OAuth discovery; empty disables OAuth endpoints")
	migrateUserID := flag.String("set-user-email-user-id", "", "one-time migration: exact existing user ID")
	migrateUsername := flag.String("set-user-email-username", "", "one-time migration: exact existing username")
	migrateEmailFile := flag.String("set-user-email-file", "", "one-time migration: 0600 file containing the confirmed email")
	// Kept as a parsed compatibility flag while deployments move to V2. The V2
	// server does not expose or start the legacy automation coordinator.
	_ = flag.String("skill-root", "", "deprecated V1 automation skill directory; ignored by the V2 server")
	automaticNotes := flag.Bool("automatic-notes", true, "index all projects automatically with one shared worker")
	notesCodex := flag.String("notes-codex", os.Getenv("EDC_NOTES_CODEX"), "authenticated Codex executable for automatic notes")
	flag.Parse()
	if flag.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	store, err := core.Open(*dbPath, *dataDir)
	if err != nil {
		return err
	}
	defer func() {
		if err := store.Close(); runErr == nil && err != nil {
			runErr = err
		}
	}()
	if *migrateUserID != "" || *migrateUsername != "" || *migrateEmailFile != "" {
		if *migrateUserID == "" || *migrateUsername == "" || *migrateEmailFile == "" {
			return fmt.Errorf("set-user-email requires user-id, username, and email-file")
		}
		email, err := readMigrationEmail(*migrateEmailFile)
		if err != nil {
			return err
		}
		user, err := store.SetUserEmail(context.Background(), *migrateUserID, *migrateUsername, email)
		if err != nil {
			return fmt.Errorf("set user email: %w", err)
		}
		slog.Info("user email migration complete", "user_id", user.ID, "username", user.Username)
		return nil
	}
	service, err := v2.New(store, *dataDir)
	if err != nil {
		return fmt.Errorf("open V2 service: %w", err)
	}
	defer func() {
		if err := service.Close(); runErr == nil && err != nil {
			runErr = err
		}
	}()
	var allowed []string
	for _, v := range strings.Split(*origins, ",") {
		if v = strings.TrimSpace(v); v != "" {
			allowed = append(allowed, v)
		}
	}
	var adminUsers []string
	for _, value := range strings.Split(os.Getenv("EDC_ADMIN_USERS"), ",") {
		if value = strings.TrimSpace(value); value != "" {
			adminUsers = append(adminUsers, value)
		}
	}
	if *publicBaseURL != "" {
		u, parseErr := url.Parse(*publicBaseURL)
		if parseErr != nil || u.Scheme != "https" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.HasSuffix(*publicBaseURL, "/") {
			return fmt.Errorf("public-base-url must be an HTTPS origin without path or trailing slash")
		}
	}
	integAuth := api.IntegAuthConfig{
		Issuer:        strings.TrimSpace(os.Getenv("EDC_INTEG_AUTH_ISSUER")),
		ClientID:      strings.TrimSpace(os.Getenv("EDC_INTEG_AUTH_CLIENT_ID")),
		ClientSecret:  strings.TrimSpace(os.Getenv("EDC_INTEG_AUTH_CLIENT_SECRET")),
		RedirectURI:   strings.TrimSpace(os.Getenv("EDC_INTEG_AUTH_REDIRECT_URI")),
		WebBaseURL:    strings.TrimSpace(os.Getenv("EDC_WEB_BASE_URL")),
		SecureCookies: envTrue("EDC_SECURE_COOKIES"),
	}
	configuredFields := 0
	for _, value := range []string{integAuth.Issuer, integAuth.ClientID, integAuth.ClientSecret, integAuth.RedirectURI, integAuth.WebBaseURL} {
		if value != "" {
			configuredFields++
		}
	}
	if configuredFields != 0 && configuredFields != 5 {
		return fmt.Errorf("EDC Integ.Auth configuration must set issuer, client ID, client secret, redirect URI, and web base URL together")
	}
	var audioTranscriber transcription.Transcriber
	if apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); apiKey != "" {
		audioTranscriber, err = transcription.NewClient(apiKey, strings.TrimSpace(os.Getenv("OPENAI_BASE_URL")), strings.TrimSpace(os.Getenv("OPENAI_TRANSCRIBE_MODEL")), 2*time.Minute)
		if err != nil {
			return fmt.Errorf("configure OpenAI transcription: %w", err)
		}
	}
	httpServer := &http.Server{Addr: *addr, Handler: api.V2HandlerWithConfig(store, service, api.Config{AllowedOrigins: allowed, PublicBaseURL: *publicBaseURL, IntegAuth: integAuth, AdminUsers: adminUsers, AudioTranscriber: audioTranscriber}), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 3 * time.Minute, WriteTimeout: 3 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *automaticNotes {
		command := *notesCodex
		if command == "" {
			command = "codex"
		}
		resolved, lookupErr := exec.LookPath(command)
		if lookupErr != nil {
			slog.Error("automatic notes unavailable: configure --notes-codex", "error", lookupErr)
		} else {
			workerCtx, cancelWorker := context.WithCancel(ctx)
			workerDone := make(chan struct{})
			go func() { defer close(workerDone); notescheduler.Run(workerCtx, store, service, resolved) }()
			defer func() { cancelWorker(); <-workerDone }()
			slog.Info("automatic notes enabled", "concurrency", 1)
		}
	}
	done := make(chan error, 1)
	go func() { done <- httpServer.Serve(ln) }()
	slog.Info("event-driven-context listening", "address", ln.Addr().String(), "mcp", "/mcp", "admin_users", len(adminUsers))
	select {
	case err = <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = httpServer.Shutdown(shutdown); err != nil {
		httpServer.Close()
		return err
	}
	return nil
}

func readMigrationEmail(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("read email migration file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 512 {
		return "", fmt.Errorf("email migration file must be a regular 0600 file no larger than 512 bytes")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read email migration file: %w", err)
	}
	return strings.TrimSpace(string(contents)), nil
}

func envTrue(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
