package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jonathanforrider/billy/internal/backend"
	"github.com/jonathanforrider/billy/internal/config"
	"github.com/jonathanforrider/billy/internal/launcher"
	"github.com/jonathanforrider/billy/internal/serve"
	"github.com/jonathanforrider/billy/internal/store"
)

func runServe() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	s, err := store.New(cfg.Storage.HistoryFile)
	if err != nil {
		// Non-fatal: serve mode can still run without history persistence.
		fmt.Fprintf(os.Stderr, "Warning: could not open history DB: %v\n", err)
	}
	if s != nil {
		defer s.Close()
	}

	b, err := backend.NewFromConfig(cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error configuring backend: %v\n", err)
		os.Exit(1)
	}

	if backend.ShouldAutoLaunchOllama(cfg) {
		launchURL := strings.TrimSpace(cfg.Backend.URL)
		if launchURL == "" {
			launchURL = "http://localhost:11434"
		}
		stopOllama, _, launchErr := launcher.EnsureRunning(ctx, launchURL)
		if launchErr != nil {
			fmt.Fprintf(os.Stderr, "\n⚠️  %s\n\n", launchErr.Error())
		} else {
			defer stopOllama()
		}
	}

	srv := serve.New(cfg, b, s, nil, version)
	if err := srv.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error running Billy serve: %v\n", err)
		os.Exit(1)
	}
}
