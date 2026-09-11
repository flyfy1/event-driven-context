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
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	addr := flag.String("addr", "127.0.0.1:8080", "HTTP listen address")
	dbPath := flag.String("db", "data/context.db", "SQLite database path")
	dataDir := flag.String("data", "data", "directory for immutable event and uploaded-file data")
	origins := flag.String("allowed-origins", "", "comma-separated browser origins; empty rejects all Origin-bearing requests")
	publicBaseURL := flag.String("public-base-url", "", "public HTTPS origin used for OAuth discovery; empty disables OAuth endpoints")
	flag.Parse()
	if flag.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	store, err := core.Open(*dbPath, *dataDir)
	if err != nil {
		return err
	}
	defer store.Close()
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
	httpServer := &http.Server{Addr: *addr, Handler: api.HandlerWithConfig(store, api.Config{AllowedOrigins: allowed, PublicBaseURL: *publicBaseURL}), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
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
