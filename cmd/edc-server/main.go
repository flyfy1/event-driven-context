package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
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
	origins := flag.String("allowed-origins", "", "comma-separated browser origins; empty rejects all Origin-bearing requests")
	flag.Parse()
	if flag.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	store, err := core.Open(*dbPath)
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
	httpServer := &http.Server{Addr: *addr, Handler: api.Handler(store, allowed), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
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
