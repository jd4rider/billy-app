package oneshot

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jonathanforrider/billy/internal/backend"
	"github.com/jonathanforrider/billy/internal/config"
	"github.com/jonathanforrider/billy/internal/memory"
	"github.com/jonathanforrider/billy/internal/store"
)

// Run executes a one-shot prompt and streams the response to stdout.
// It handles special subcommands: read, explain, fix, run.
func Run(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		cfg = config.DefaultConfig()
	}

	// Init store for memory context
	var s *store.Store
	if cfg.Storage.HistoryFile != "" {
		s, _ = store.New(cfg.Storage.HistoryFile)
		if s != nil {
			defer s.Close()
		}
	}

	b := backend.NewOllama(cfg.Backend.URL, cfg.Ollama.Model)

	subcommand := strings.ToLower(args[0])
	switch subcommand {
	case "read":
		return runRead(args[1:], cfg, b, s)
	case "explain":
		return runExplain(args[1:], cfg, b, s)
	case "fix":
		return runFix(args[1:], cfg, b, s)
	case "run":
		return runExec(args[1:], cfg, b, s)
	default:
		// Treat entire args as a natural language prompt
		prompt := strings.Join(args, " ")
		// Check if stdin has data (piped input)
		stdinData := readStdin()
		if stdinData != "" {
			prompt = prompt + "\n\n```\n" + stdinData + "\n```"
		}
		return chat(prompt, cfg, b, s)
	}
}

// readStdin reads from stdin if it's a pipe (not a terminal).
func readStdin() string {
	stat, err := os.Stdin.Stat()
	if err != nil || (stat.Mode()&os.ModeCharDevice) != 0 {
		return "" // terminal, not a pipe
	}
	data, _ := io.ReadAll(os.Stdin)
	return strings.TrimSpace(string(data))
}

// runRead summarizes a file or directory.
func runRead(args []string, cfg *config.Config, b backend.Backend, s *store.Store) error {
	if len(args) == 0 {
		args = []string{"."}
	}
	target := args[0]
	content, err := readTarget(target)
	if err != nil {
		return err
	}
	prompt := fmt.Sprintf("Analyze the following codebase/file and provide a concise report covering: purpose, architecture, key components, dependencies, and any notable patterns or issues.\n\nPath: %s\n\n```\n%s\n```", target, content)
	return chat(prompt, cfg, b, s)
}

// runExplain explains what a file does.
func runExplain(args []string, cfg *config.Config, b backend.Backend, s *store.Store) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: billy explain <file>")
	}
	content, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	prompt := fmt.Sprintf("Explain what this code does in plain English. Be concise and focus on the key logic.\n\nFile: %s\n\n```\n%s\n```", args[0], string(content))
	return chat(prompt, cfg, b, s)
}

// runFix suggests and optionally applies fixes to a file.
func runFix(args []string, cfg *config.Config, b backend.Backend, s *store.Store) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: billy fix <file>")
	}
	content, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	prompt := fmt.Sprintf("Review this code for bugs, errors, and improvements. Provide the fixed version in a code block with the full corrected file content.\n\nFile: %s\n\n```\n%s\n```", args[0], string(content))
	return chat(prompt, cfg, b, s)
}

// runExec runs a file with AI context, showing output.
func runExec(args []string, cfg *config.Config, b backend.Backend, s *store.Store) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: billy run <file> [args...]")
	}
	file := args[0]
	fmt.Printf("▶ Running: %s\n", strings.Join(args, " "))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, args[0], args[1:]...) //nolint:gosec
	var out bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &out)
	cmd.Stderr = io.MultiWriter(os.Stderr, &out)
	runErr := cmd.Run()

	if runErr != nil {
		fmt.Printf("\n⚠ Exited with error: %v\n", runErr)
		fmt.Println("\n🤔 Asking Billy what went wrong...\n")
		content, _ := os.ReadFile(file)
		prompt := fmt.Sprintf("This program exited with an error. Here's the source code and the output. Diagnose the problem and suggest a fix.\n\nFile: %s\n```\n%s\n```\n\nOutput:\n```\n%s\n```\n\nError: %v", file, string(content), out.String(), runErr)
		return chat(prompt, cfg, b, s)
	}
	return nil
}

// readTarget reads a file or recursively reads a directory (up to 100KB total).
func readTarget(target string) (string, error) {
	info, err := os.Stat(target)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		data, err := os.ReadFile(target)
		return string(data), err
	}

	var sb strings.Builder
	var totalBytes int
	const maxBytes = 100 * 1024 // 100KB cap

	err = filepath.WalkDir(target, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		// Skip binary files, vendor, node_modules, .git
		for _, skip := range []string{".git", "node_modules", "vendor", ".next", "dist", "build"} {
			if strings.Contains(path, "/"+skip+"/") || strings.Contains(path, string(os.PathSeparator)+skip+string(os.PathSeparator)) {
				return nil
			}
		}
		ext := strings.ToLower(filepath.Ext(path))
		textExts := map[string]bool{".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".py": true, ".rs": true, ".md": true, ".yaml": true, ".yml": true, ".toml": true, ".json": true, ".sh": true, ".dockerfile": true, ".tf": true, ".sql": true, ".html": true, ".css": true}
		if !textExts[ext] && ext != "" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil || totalBytes+len(data) > maxBytes {
			return nil
		}
		totalBytes += len(data)
		sb.WriteString(fmt.Sprintf("\n// === %s ===\n%s\n", path, string(data)))
		return nil
	})
	return sb.String(), err
}

// chat sends a prompt to the backend and streams the response to stdout.
func chat(prompt string, cfg *config.Config, b backend.Backend, s *store.Store) error {
	// Build system prompt with memories
	var memTexts []string
	if s != nil {
		if mems, err := s.ListMemories(); err == nil {
			for _, mem := range mems {
				memTexts = append(memTexts, mem.Content)
			}
		}
	}
	systemPrompt := memory.BuildSystemPrompt(memTexts)

	history := []backend.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: prompt},
	}

	opts := backend.ChatOptions{
		Temperature: cfg.Ollama.Temperature,
		NumPredict:  cfg.Ollama.NumPredict,
	}

	fmt.Print("\033[32mBilly\033[0m\n") // green "Billy" header
	response, err := b.Chat(context.Background(), history, opts)
	if err != nil {
		return fmt.Errorf("chat error: %w", err)
	}

	// Stream word-by-word for a nicer effect (response is already complete)
	scanner := bufio.NewScanner(strings.NewReader(response))
	for scanner.Scan() {
		fmt.Println(scanner.Text())
	}
	fmt.Println()
	return nil
}
