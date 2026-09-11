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
	"os/signal"
	"strings"
	"syscall"
	"time"

	"event-driven-context/internal/api"
	"event-driven-context/internal/core"
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
	// Kept as a parsed compatibility flag while deployments move to V2. The V2
	// server does not expose or start the legacy automation coordinator.
	_ = flag.String("skill-root", "", "deprecated V1 automation skill directory; ignored by the V2 server")
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
	if *publicBaseURL != "" {
		u, parseErr := url.Parse(*publicBaseURL)
		if parseErr != nil || u.Scheme != "https" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.HasSuffix(*publicBaseURL, "/") {
			return fmt.Errorf("public-base-url must be an HTTPS origin without path or trailing slash")
		}
	}
	httpServer := &http.Server{Addr: *addr, Handler: api.V2HandlerWithConfig(store, service, api.Config{AllowedOrigins: allowed, PublicBaseURL: *publicBaseURL}), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 3 * time.Minute, WriteTimeout: 3 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- httpServer.Serve(ln) }()
	slog.Info("event-driven-context listening", "address", ln.Addr().String(), "mcp", "/mcp")
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
