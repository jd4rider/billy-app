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
	"sync"
	"time"

	"github.com/jonathanforrider/billy/internal/backend"
	"github.com/jonathanforrider/billy/internal/config"
	"github.com/jonathanforrider/billy/internal/memory"
	"github.com/jonathanforrider/billy/internal/store"
)

type Options struct {
	Agent bool
	Yolo  bool
}

const maxAgentTurns = 6
const backendRequestTimeout = 90 * time.Second
const pwdMarker = "__BILLY_PWD__:"

const agentSystemPrompt = `You are Billy, an agentic AI coding assistant running locally.

AGENT MODE is active. Your job is to take action, not just advise.

Rules:
- When shell commands are needed, provide the exact commands in ` + "```bash" + ` code blocks.
- Keep commands small and sequential when possible.
- After commands run, their output and the updated working directory will be fed back to you. Use that to continue, debug, or adjust.
- When the user's request has been satisfied and verification passes, stop. Do not output more bash blocks. Briefly say the task is complete.
- Do not keep re-running the same verification step after it has already succeeded.
- Be concise. Prefer action over explanation.
- Warn clearly before anything destructive.`

// Run executes a one-shot prompt and streams the response to stdout.
// It handles special subcommands: read, explain, fix, run.
func Run(args []string, opts Options) error {
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

	b, err := backend.NewFromConfig(cfg, nil)
	if err != nil {
		return err
	}

	subcommand := strings.ToLower(args[0])
	switch subcommand {
	case "read":
		return runRead(args[1:], cfg, b, s, opts)
	case "explain":
		return runExplain(args[1:], cfg, b, s, opts)
	case "fix":
		return runFix(args[1:], cfg, b, s, opts)
	case "run":
		return runExec(args[1:], cfg, b, s)
	default:
		// Treat entire args as a natural language prompt
		prompt := strings.Join(args, " ")
		// Check if stdin has data (piped input)
		stdinData := readStdin()
		if stdinData != "" {
			prompt = prompt + "\n\n```\n" + stdinData + "\n```"
		} else if shouldAttachRepoContext(prompt) {
			prompt = augmentPromptWithRepoContext(prompt)
		}
		return runPrompt(prompt, cfg, b, s, opts.Agent, opts.Yolo)
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
func runRead(args []string, cfg *config.Config, b backend.Backend, s *store.Store, opts Options) error {
	if len(args) == 0 {
		args = []string{"."}
	}
	target := args[0]
	content, err := readTarget(target)
	if err != nil {
		return err
	}
	prompt := fmt.Sprintf("Analyze the following codebase/file and provide a concise report covering: purpose, architecture, key components, dependencies, and any notable patterns or issues.\n\nPath: %s\n\n```\n%s\n```", target, content)
	return runPrompt(prompt, cfg, b, s, opts.Agent, opts.Yolo)
}

// runExplain explains what a file does.
func runExplain(args []string, cfg *config.Config, b backend.Backend, s *store.Store, opts Options) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: billy explain <file>")
	}
	content, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	prompt := fmt.Sprintf("Explain what this code does in plain English. Be concise and focus on the key logic.\n\nFile: %s\n\n```\n%s\n```", args[0], string(content))
	return runPrompt(prompt, cfg, b, s, opts.Agent, opts.Yolo)
}

// runFix suggests and optionally applies fixes to a file.
func runFix(args []string, cfg *config.Config, b backend.Backend, s *store.Store, opts Options) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: billy fix <file>")
	}
	content, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	prompt := fmt.Sprintf("Review this code for bugs, errors, and improvements. Provide the fixed version in a code block with the full corrected file content.\n\nFile: %s\n\n```\n%s\n```", args[0], string(content))
	return runPrompt(prompt, cfg, b, s, opts.Agent, opts.Yolo)
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
		fmt.Print("\n🤔 Asking Billy what went wrong...\n\n")
		content, _ := os.ReadFile(file)
		prompt := fmt.Sprintf("This program exited with an error. Here's the source code and the output. Diagnose the problem and suggest a fix.\n\nFile: %s\n```\n%s\n```\n\nOutput:\n```\n%s\n```\n\nError: %v", file, string(content), out.String(), runErr)
		return runPrompt(prompt, cfg, b, s, false, false)
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

func shouldAttachRepoContext(prompt string) bool {
	p := strings.ToLower(strings.TrimSpace(prompt))
	repoHints := []string{
		"this repository",
		"this repo",
		"this codebase",
		"current repository",
		"current repo",
		"current codebase",
		"this project",
		"current project",
	}
	for _, hint := range repoHints {
		if strings.Contains(p, hint) {
			return true
		}
	}
	return false
}

func augmentPromptWithRepoContext(userPrompt string) string {
	target := "."
	if root := findGitRoot("."); root != "" {
		target = root
	}

	snapshot, err := readTarget(target)
	if err != nil || strings.TrimSpace(snapshot) == "" {
		return userPrompt
	}

	var sb strings.Builder
	sb.WriteString("The user is asking about the repository in the current working directory. ")
	sb.WriteString("Use the repository snapshot and git summary below to answer directly. ")
	sb.WriteString("Do not ask for a URL or more repository context unless the snapshot is clearly insufficient.\n\n")
	sb.WriteString(fmt.Sprintf("Working directory: %s\n", target))

	if gitSummary := gitRepoSummary(target); gitSummary != "" {
		sb.WriteString("\nGit summary:\n")
		sb.WriteString(gitSummary)
		sb.WriteString("\n")
	}

	sb.WriteString("\nRepository snapshot:\n```\n")
	sb.WriteString(snapshot)
	sb.WriteString("\n```\n\n")
	sb.WriteString("User request: ")
	sb.WriteString(userPrompt)
	return sb.String()
}

func findGitRoot(dir string) string {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func gitRepoSummary(dir string) string {
	parts := []struct {
		label string
		args  []string
	}{
		{label: "Branch", args: []string{"rev-parse", "--abbrev-ref", "HEAD"}},
		{label: "Status", args: []string{"status", "--short", "--branch"}},
		{label: "Recent commits", args: []string{"log", "--oneline", "-5"}},
	}

	var sb strings.Builder
	for _, part := range parts {
		cmd := exec.Command("git", append([]string{"-C", dir}, part.args...)...) //nolint:gosec
		out, err := cmd.Output()
		if err != nil {
			continue
		}
		text := strings.TrimSpace(string(out))
		if text == "" {
			continue
		}
		sb.WriteString(part.label)
		sb.WriteString(":\n")
		sb.WriteString(text)
		sb.WriteString("\n\n")
	}
	return strings.TrimSpace(sb.String())
}

func isTerminalFile(f *os.File) bool {
	if f == nil {
		return false
	}
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}

func startThinkingSpinner(label string) func() {
	if !isTerminalFile(os.Stderr) {
		return func() {}
	}

	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	done := make(chan struct{})

	go func() {
		ticker := time.NewTicker(90 * time.Millisecond)
		defer ticker.Stop()

		i := 0
		for {
			fmt.Fprintf(os.Stderr, "\r%s %s", frames[i], label)
			select {
			case <-done:
				fmt.Fprint(os.Stderr, "\r\033[K")
				return
			case <-ticker.C:
				i = (i + 1) % len(frames)
			}
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
		})
	}
}

func buildSystemPrompt(s *store.Store, agent bool) string {
	var memTexts []string
	if s != nil {
		if mems, err := s.ListMemories(); err == nil {
			for _, mem := range mems {
				memTexts = append(memTexts, mem.Content)
			}
		}
	}
	systemPrompt := memory.BuildSystemPrompt(memTexts)
	if agent {
		systemPrompt = agentSystemPrompt + "\n\n" + systemPrompt
	}
	return systemPrompt
}

func streamAssistantReply(history []backend.Message, cfg *config.Config, b backend.Backend) (string, error) {
	opts := backend.ChatOptions{
		Temperature: cfg.Ollama.Temperature,
		NumPredict:  cfg.Ollama.NumPredict,
		Stream:      true,
	}

	fmt.Print("\033[32mBilly\033[0m\n")
	stopSpinner := startThinkingSpinner("Billy is thinking...")
	defer stopSpinner()

	var response string
	printed := false
	var once sync.Once

	ctx, cancel := context.WithTimeout(context.Background(), backendRequestTimeout)
	defer cancel()

	response, err := b.StreamChat(ctx, history, opts, func(token string) {
		once.Do(stopSpinner)
		if token == "" {
			return
		}
		printed = true
		fmt.Fprint(os.Stdout, token)
	})
	if err != nil {
		return "", fmt.Errorf("chat error: %w", err)
	}

	if !printed && response != "" {
		scanner := bufio.NewScanner(strings.NewReader(response))
		for scanner.Scan() {
			fmt.Println(scanner.Text())
		}
		printed = true
	}

	if printed && !strings.HasSuffix(response, "\n") {
		fmt.Println()
	}
	if !printed {
		fmt.Println()
	}
	return response, nil
}

func runPrompt(prompt string, cfg *config.Config, b backend.Backend, s *store.Store, agent, yolo bool) error {
	if agent {
		return runAgent(prompt, cfg, b, s, yolo)
	}
	return chat(prompt, cfg, b, s)
}

// chat sends a prompt to the backend and streams the response to stdout.
func chat(prompt string, cfg *config.Config, b backend.Backend, s *store.Store) error {
	history := []backend.Message{
		{Role: "system", Content: buildSystemPrompt(s, false)},
		{Role: "user", Content: prompt},
	}
	_, err := streamAssistantReply(history, cfg, b)
	if err != nil {
		return fmt.Errorf("chat error: %w", err)
	}
	return nil
}

func shouldStopAgentLoop(records []string) bool {
	if len(records) == 0 {
		return false
	}
	if recordBatchHasError(records) {
		return false
	}
	for _, record := range records {
		if recordShowsSuccessfulVerification(record) {
			return true
		}
	}
	return false
}

func normalizeCommandBatch(cmds []string) string {
	cleaned := make([]string, 0, len(cmds))
	for _, cmd := range cmds {
		cmd = strings.TrimSpace(strings.ReplaceAll(cmd, "\r\n", "\n"))
		if cmd != "" {
			cleaned = append(cleaned, cmd)
		}
	}
	return strings.Join(cleaned, "\n---\n")
}

func normalizeRecordBatch(records []string) string {
	cmds := make([]string, 0, len(records))
	for _, record := range records {
		cmds = append(cmds, extractShellCommands(record)...)
	}
	return normalizeCommandBatch(cmds)
}

func recordBatchHasError(records []string) bool {
	for _, record := range records {
		if strings.Contains(strings.ToLower(record), "exit error:") {
			return true
		}
	}
	return false
}

func splitShellSteps(block string) []string {
	normalized := strings.NewReplacer("&&", "\n", ";", "\n", "||", "\n").Replace(block)
	rawLines := strings.Split(normalized, "\n")
	steps := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		steps = append(steps, line)
	}
	return steps
}

func stepMatchesAnyPrefix(step string, prefixes []string) bool {
	step = strings.ToLower(strings.TrimSpace(step))
	for _, prefix := range prefixes {
		if strings.HasPrefix(step, prefix) {
			return true
		}
	}
	return false
}

func outputLooksSuccessful(text string) bool {
	hints := []string{
		"pass", "passed", "success", "successful", "ok\t", "ok ", "compiled successfully",
		"build completed", "ready in", "local:", "localhost", "127.0.0.1", "listening on",
		"server running", "application started", "done in", "finished in", "<!doctype html",
		"<html", "200 ok", "http/1.1 200", "http/2 200", "vite v", "webpack compiled",
	}
	for _, hint := range hints {
		if strings.Contains(text, hint) {
			return true
		}
	}
	return false
}

func recordShowsSuccessfulVerification(record string) bool {
	blocks := extractShellCommands(record)
	if len(blocks) == 0 {
		return false
	}

	immediatePrefixes := []string{
		"go test", "go build",
		"npm test", "npm run test", "npm run build",
		"pnpm test", "pnpm build",
		"yarn test", "yarn build",
		"bun test", "bun run build",
		"cargo test", "cargo build",
		"pytest", "python -m pytest", "python3 -m pytest",
		"next build",
	}

	runtimePrefixes := []string{
		"go run",
		"npm run dev", "npm start",
		"pnpm dev", "pnpm start",
		"yarn dev", "yarn start",
		"bun run dev", "bun run start",
		"cargo run",
		"uv run", "uvicorn", "vite", "next dev",
		"curl ", "wget ", "python ", "python3 ",
	}

	recordLower := strings.ToLower(record)
	hasImmediate := false
	hasRuntime := false
	for _, block := range blocks {
		for _, step := range splitShellSteps(block) {
			switch {
			case stepMatchesAnyPrefix(step, immediatePrefixes):
				hasImmediate = true
			case stepMatchesAnyPrefix(step, runtimePrefixes):
				hasRuntime = true
			}
		}
	}

	if hasRuntime {
		return outputLooksSuccessful(recordLower)
	}
	return hasImmediate
}

func formatCommandRecord(shellCmd, startDir, endDir, output string, runErr error) string {
	output = strings.TrimRight(output, "\n")
	if output == "" {
		output = "(no output)"
	}

	var sb strings.Builder
	sb.WriteString("Command block:\n```bash\n")
	sb.WriteString(shellCmd)
	sb.WriteString("\n```\n")
	if startDir != "" {
		sb.WriteString("\nWorking directory before command:\n```text\n")
		sb.WriteString(startDir)
		sb.WriteString("\n```\n")
	}
	if endDir != "" {
		sb.WriteString("\nWorking directory after command:\n```text\n")
		sb.WriteString(endDir)
		sb.WriteString("\n```\n")
	}
	if runErr != nil {
		sb.WriteString("\nExit error: ")
		sb.WriteString(runErr.Error())
		sb.WriteString("\n")
	}
	sb.WriteString("\nCommand output:\n```text\n")
	sb.WriteString(output)
	sb.WriteString("\n```")
	return sb.String()
}

func runAgent(prompt string, cfg *config.Config, b backend.Backend, s *store.Store, yolo bool) error {
	history := []backend.Message{
		{Role: "system", Content: buildSystemPrompt(s, true)},
		{Role: "user", Content: prompt},
	}

	allowAll := yolo
	workDir, _ := os.Getwd()
	lastSuccessfulSig := ""
	for turn := 0; turn < maxAgentTurns; turn++ {
		response, err := streamAssistantReply(history, cfg, b)
		if err != nil {
			return fmt.Errorf("agent error: %w", err)
		}

		history = append(history, backend.Message{Role: "assistant", Content: response})
		cmds := extractShellCommands(response)
		if len(cmds) == 0 {
			return nil
		}
		sig := normalizeCommandBatch(cmds)
		if sig != "" && sig == lastSuccessfulSig {
			fmt.Fprintln(os.Stderr, "Autopilot stopped to avoid repeating the same successful command batch.")
			return nil
		}

		records, changedAllowAll, continueLoop, nextDir, hadError, err := reviewAndRunCommands(cmds, allowAll, workDir)
		if err != nil {
			return err
		}
		allowAll = changedAllowAll
		workDir = nextDir
		if !continueLoop {
			return nil
		}
		if len(records) == 0 {
			return nil
		}
		if !hadError {
			lastSuccessfulSig = sig
		} else {
			lastSuccessfulSig = ""
		}
		if shouldStopAgentLoop(records) {
			fmt.Fprintln(os.Stderr, "Autopilot verified a successful result and stopped.")
			return nil
		}

		history = append(history, backend.Message{
			Role:    "user",
			Content: strings.Join(records, "\n\n") + fmt.Sprintf("\n\nCurrent working directory for the next step:\n```text\n%s\n```\n\nDecide whether the user's task is now complete. If it is complete, do not suggest any more commands. Briefly explain that it is done and stop. Only produce another bash block if more work is truly required.", workDir),
		})
	}

	return fmt.Errorf("agent stopped after %d turns; rerun with a narrower prompt", maxAgentTurns)
}

func reviewAndRunCommands(cmds []string, allowAll bool, workDir string) ([]string, bool, bool, string, bool, error) {
	var records []string
	hadError := false
	currentDir := workDir
	for _, cmd := range cmds {
		runThis := allowAll
		if !allowAll {
			approved, always, quit, err := promptApproveCommand(cmd)
			if err != nil {
				return nil, allowAll, false, currentDir, hadError, err
			}
			if quit {
				return records, allowAll, false, currentDir, hadError, nil
			}
			if !approved {
				fmt.Fprintln(os.Stderr, "Skipped command block.")
				return records, allowAll, false, currentDir, hadError, nil
			}
			runThis = true
			if always {
				allowAll = true
			}
		}

		if runThis {
			record, nextDir, cmdFailed, err := runShellCommand(currentDir, cmd)
			if err != nil {
				return nil, allowAll, false, currentDir, hadError, err
			}
			currentDir = nextDir
			hadError = hadError || cmdFailed
			records = append(records, record)
		}
	}
	return records, allowAll, true, currentDir, hadError, nil
}

func promptApproveCommand(cmd string) (approved, always, quit bool, err error) {
	if !isTerminalFile(os.Stdin) {
		return false, false, false, fmt.Errorf("--agent requires an interactive terminal to approve commands")
	}

	reader := bufio.NewReader(os.Stdin)
	fmt.Fprintln(os.Stderr, "\nRun command block?")
	fmt.Fprintln(os.Stderr, "----------------------------------------")
	fmt.Fprintln(os.Stderr, cmd)
	fmt.Fprintln(os.Stderr, "----------------------------------------")
	fmt.Fprint(os.Stderr, "[y]es / [a]lways / [n]o / [q]uit > ")

	line, err := reader.ReadString('\n')
	if err != nil {
		return false, false, false, err
	}

	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes", "":
		return true, false, false, nil
	case "a", "always":
		return true, true, false, nil
	case "q", "quit":
		return false, false, true, nil
	default:
		return false, false, false, nil
	}
}

func runShellCommand(workDir, shellCmd string) (record, nextDir string, failed bool, err error) {
	fmt.Fprintf(os.Stderr, "\n\033[33mCommand\033[0m\n%s\n\n", shellCmd)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	startDir := workDir
	output, nextDir, err := runShellCommandInDir(ctx, workDir, shellCmd)
	if output == "" {
		output = "(no output)"
	}
	if !strings.HasSuffix(output, "\n") {
		output += "\n"
	}
	fmt.Fprintln(os.Stderr)

	return formatCommandRecord(shellCmd, startDir, nextDir, output, err), nextDir, err != nil, nil
}

func runShellCommandInDir(ctx context.Context, workDir, shellCmd string) (output, finalDir string, err error) {
	wrapped := fmt.Sprintf("{ %s; }; status=$?; printf '\n%s%%s\n' \"$PWD\"; exit $status", shellCmd, pwdMarker)
	cmd := exec.CommandContext(ctx, "sh", "-c", wrapped) //nolint:gosec
	if workDir != "" {
		cmd.Dir = workDir
	}

	var out bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &out)
	cmd.Stderr = io.MultiWriter(os.Stderr, &out)

	err = cmd.Run()
	output = out.String()
	finalDir = workDir

	if idx := strings.LastIndex(output, pwdMarker); idx >= 0 {
		pwdText := strings.TrimSpace(output[idx+len(pwdMarker):])
		if pwdText != "" {
			finalDir = pwdText
		}
		output = strings.TrimRight(output[:idx], "\n")
	}
	return output, finalDir, err
}

func extractShellCommands(content string) []string {
	var cmds []string
	lines := strings.Split(content, "\n")
	inBlock := false
	var block strings.Builder

	for _, line := range lines {
		if !inBlock {
			stripped := strings.TrimSpace(line)
			if strings.HasPrefix(stripped, "```") {
				lang := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(stripped, "```")))
				if lang == "bash" || lang == "sh" || lang == "shell" || lang == "zsh" {
					inBlock = true
					block.Reset()
				}
			}
			continue
		}
		if strings.TrimSpace(line) == "```" {
			inBlock = false
			cmd := strings.TrimSpace(block.String())
			if cmd != "" {
				cmds = append(cmds, cmd)
			}
			continue
		}
		block.WriteString(line)
		block.WriteByte('\n')
	}
	return cmds
}
