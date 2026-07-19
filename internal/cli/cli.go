// Package cli implements the agent-api-gateway command line.
package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/sargunv/agent-api-gateway/internal/config"
	"github.com/sargunv/agent-api-gateway/internal/provider"
	"github.com/sargunv/agent-api-gateway/internal/server"
	"github.com/spf13/cobra"
)

var (
	Version = "0.0.0-dev"
	Commit  = "unknown"
)

func Execute() error { return root().Execute() }
func root() *cobra.Command {
	r := &cobra.Command{Use: "agent-api-gateway", Short: "Portable single-key subscription API gateway", Version: Version, SilenceUsage: true}
	r.AddCommand(serve())
	return r
}

func serve() *cobra.Command {
	var addr, auth string
	c := &cobra.Command{Use: "serve", Short: "Run the gateway", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error { return run(cmd.Context(), addr, auth) }}
	c.Flags().StringVarP(&addr, "addr", "a", "", "listen address (default GATEWAY_ADDR or 127.0.0.1:8080)")
	c.Flags().StringVar(&auth, "auth-file", "", "Codex auth.json path (equivalent to CODEX_AUTH_FILE)")
	return c
}

func run(ctx context.Context, addr, auth string) error {
	get := func(k string) string {
		if k == "GATEWAY_ADDR" && addr != "" {
			return addr
		}
		if k == "CODEX_AUTH_FILE" && auth != "" {
			return auth
		}
		return os.Getenv(k)
	}
	c, err := config.Load(get)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	discoveryCtx, cancelDiscovery := context.WithTimeout(ctx, 15*time.Second)
	warnings := provider.Discover(discoveryCtx, c.Accounts, nil)
	cancelDiscovery()
	for _, warning := range warnings {
		logger.Warn("model catalog reconciliation",
			"provider", warning.Provider,
			"model", warning.Model,
			"kind", warning.Kind,
			"detail", warning.Detail,
		)
	}
	s, err := server.New(c, nil, logger)
	if err != nil {
		return err
	}
	s.Inventory()
	srv := s.HTTP(c.Addr)
	listener, err := net.Listen("tcp", c.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer func() { _ = listener.Close() }()
	if err = publishReady(c.ReadyFile, listener.Addr().String()); err != nil {
		return fmt.Errorf("publish readiness: %w", err)
	}
	if c.ReadyFile != "" {
		defer func() { _ = os.Remove(c.ReadyFile) }()
	}
	stop, done := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer done()
	errc := make(chan error, 1)
	go func() {
		logger.Info("gateway listening", "version", Version, "commit", Commit, "addr", listener.Addr().String(), "models", len(c.Accounts))
		errc <- srv.Serve(listener)
	}()
	select {
	case err = <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-stop.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err = srv.Shutdown(shutdown); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		err = <-errc
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func publishReady(path, addr string) error {
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if err = f.Chmod(0o600); err != nil {
		return err
	}
	if _, err = fmt.Fprintln(f, addr); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	ok = true
	return nil
}
