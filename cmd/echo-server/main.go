package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/vancanhuit/echo-server/internal/buildinfo"
	"github.com/vancanhuit/echo-server/internal/server"
)

func main() {
	if err := run(); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("echo-server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "-version") {
		fmt.Printf("echo-server %s (commit %s, built %s)\n", buildinfo.Version, buildinfo.Commit, buildinfo.Date)
		return nil
	}
	cfg, err := server.LoadConfig(os.Args[1:], os.Getenv, os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	logger := cfg.Logger(os.Stdout)
	logger.Info("starting", "version", buildinfo.Version, "commit", buildinfo.Commit, "build_date", buildinfo.Date)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var shutdownErr error
	start := echo.StartConfig{
		Address:         ":" + strconv.Itoa(cfg.Port),
		HideBanner:      true,
		GracefulTimeout: 10 * time.Second,
		OnShutdownError: func(err error) { shutdownErr = err },
		BeforeServeFunc: func(s *http.Server) error {
			s.ReadHeaderTimeout = 5 * time.Second
			s.IdleTimeout = 60 * time.Second
			return nil
		},
	}
	err = start.Start(ctx, server.New(cfg, logger))
	return errors.Join(err, shutdownErr)
}
