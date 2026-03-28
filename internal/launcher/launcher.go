package launcher

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// Result describes the state of Ollama after EnsureRunning.
type Result struct {
	AlreadyRunning bool
	Started        bool
	Bundled        bool // fat build used embedded binary
	BinaryPath     string
}

const billyBinDir = ".billy/bin"

// EnsureRunning checks if Ollama is reachable and starts it if needed.
// For fat builds, it extracts the embedded binary first.
// For slim builds, it searches ~/.billy/bin then PATH.
// Returns a stop func to kill the started process, the result, and any error.
func EnsureRunning(ctx context.Context, serverURL string) (stop func(), result Result, err error) {
	noop := func() {}

	if isReachable(ctx, serverURL) {
		return noop, Result{AlreadyRunning: true}, nil
	}

	var binPath string

	if len(embeddedOllama) > 0 {
		// Fat build: extract embedded binary then start it
		binPath, err = extractEmbedded()
		if err != nil {
			return noop, result, fmt.Errorf("extracting embedded ollama: %w", err)
		}
		result.Bundled = true
	} else {
		// Slim build: look in ~/.billy/bin then PATH
		var found bool
		binPath, found = findOllamaBinary()
		if !found {
			return noop, result, notFoundError()
		}
	}

	result.BinaryPath = binPath
	stop, err = startProcess(ctx, binPath, serverURL)
	if err != nil {
		return noop, result, err
	}
	result.Started = true
	return stop, result, nil
}

// IsOllamaRunning returns true if Ollama is reachable at the given URL.
func IsOllamaRunning(ctx context.Context, serverURL string) bool {
	return isReachable(ctx, serverURL)
}

func isReachable(ctx context.Context, serverURL string) bool {
	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, serverURL, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

// findOllamaBinary checks ~/.billy/bin/ollama first, then system PATH.
func findOllamaBinary() (string, bool) {
	home, err := os.UserHomeDir()
	if err == nil {
		localPath := filepath.Join(home, billyBinDir, ollamaBinName())
		if _, err := os.Stat(localPath); err == nil {
			return localPath, true
		}
	}
	if path, err := exec.LookPath(ollamaBinName()); err == nil {
		return path, true
	}
	return "", false
}

func ollamaBinName() string {
	if runtime.GOOS == "windows" {
		return "ollama.exe"
	}
	return "ollama"
}

// startProcess starts `<binPath> serve` and polls serverURL for up to 6s.
func startProcess(ctx context.Context, binPath, serverURL string) (stop func(), err error) {
	cmd := exec.CommandContext(ctx, binPath, "serve")
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return func() {}, fmt.Errorf("starting ollama: %w", err)
	}
	stop = func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
	// Poll until reachable or timeout
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if isReachable(ctx, serverURL) {
			return stop, nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return stop, fmt.Errorf("ollama did not become ready within 6s")
}

func notFoundError() error {
	return fmt.Errorf(`Ollama is not installed or not running.

  Install Ollama:        https://ollama.com

  After installing Ollama, run:  ollama pull qwen2.5-coder:14b`)
}
