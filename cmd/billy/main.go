package main

import (
	"context"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonathanforrider/billy/internal/backend"
	"github.com/jonathanforrider/billy/internal/config"
	"github.com/jonathanforrider/billy/internal/launcher"
	"github.com/jonathanforrider/billy/internal/oneshot"
	"github.com/jonathanforrider/billy/internal/store"
	"github.com/jonathanforrider/billy/internal/tui"
)

var version = "dev"
var commit = "unknown"
var date = "unknown"

func main() {
	// Handle --version / -version before anything else
	for _, arg := range os.Args[1:] {
		if arg == "--version" || arg == "-version" {
			fmt.Printf("billy version %s (%s) built %s [%s]\n", version, commit, date, launcher.BuildVariant)
			os.Exit(0)
		}
	}

	// One-shot mode: if non-flag args remain, run headlessly
	var promptArgs []string
	for _, a := range os.Args[1:] {
		if a != "--version" && a != "-version" {
			promptArgs = append(promptArgs, a)
		}
	}
	if len(promptArgs) > 0 {
		if err := oneshot.Run(promptArgs); err != nil {
			fmt.Fprintf(os.Stderr, "billy: %v\n", err)
			os.Exit(1)
		}
		return
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	s, err := store.New(cfg.Storage.HistoryFile)
	if err != nil {
		// Non-fatal: run without persistence
		fmt.Fprintf(os.Stderr, "Warning: could not open history DB: %v\n", err)
	}
	if s != nil {
		defer s.Close()
	}

	b := backend.NewOllama(cfg.Backend.URL, cfg.Ollama.Model)

	stopOllama, _, launchErr := launcher.EnsureRunning(context.Background(), cfg.Backend.URL)
	if launchErr != nil {
		fmt.Fprintf(os.Stderr, "\n⚠️  %s\n\n", launchErr.Error())
	} else {
		defer stopOllama()
	}

	m := tui.New(cfg, b, s)

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running Billy: %v\n", err)
		os.Exit(1)
	}
}
