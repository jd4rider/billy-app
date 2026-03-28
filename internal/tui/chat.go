package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/google/uuid"
	"github.com/muesli/reflow/wordwrap"

	"github.com/jonathanforrider/billy/internal/backend"
	"github.com/jonathanforrider/billy/internal/config"
	"github.com/jonathanforrider/billy/internal/license"
	"github.com/jonathanforrider/billy/internal/memory"
	"github.com/jonathanforrider/billy/internal/store"
)

// Styles
var (
	userStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("12")).
			Bold(true)

	assistantStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")).
			Bold(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("9")).
			Bold(true)

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))

	borderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("5"))
)

const defaultPromptPlaceholder = "Ask Billy anything... (/help, /model, Ctrl+D to quit)"
const backendRequestTimeout = 90 * time.Second
const pwdMarker = "__BILLY_PWD__:"

type pickerItem struct {
	cmd     string
	desc    string
	hasArgs bool // true = fill with trailing space; false = execute immediately on Enter
}

var commandList = []pickerItem{
	{"/admin", "Admin controls: PIN, mode lock, curriculum", true},
	{"/backend", "Show backend status or reload backend config", false},
	{"/cd", "Change working directory (autocompletes paths)", true},
	{"/clear", "Clear the current chat", false},
	{"/compact", "Summarize and compress context", false},
	{"/explain", "Explain what a shell command does", true},
	{"/git", "Show git status and recent commits", false},
	{"/help", "Show all commands", false},
	{"/hint", "Request a more specific hint (teach mode)", false},
	{"/history", "Browse past conversations", false},
	{"/license", "Show Billy's current open-core access model", false},
	{"/ls", "List files in current (or given) directory", true},
	{"/memory", "List or manage memories", false},
	{"/mode", "Switch between agent, chat, and teach mode", true},
	{"/model", "List or switch Ollama models", true},
	{"/pull", "Download a model from Ollama", true},
	{"/pwd", "Print current working directory", false},
	{"/quit", "Exit Billy", false},
	{"/resume", "Load a past conversation by ID", true},
	{"/run", "Run a shell command (with permission prompt)", true},
	{"/save", "Save current conversation", false},
	{"/session", "Save a session checkpoint", false},
	{"/suggest", "Suggest a shell command for a task", true},
	{"/yolo", "Toggle auto-approve for all commands this session", false},
}

func filterCommands(input string) []pickerItem {
	if input == "/" {
		return commandList
	}
	var out []pickerItem
	for _, c := range commandList {
		if strings.HasPrefix(c.cmd, input) {
			out = append(out, c)
		}
	}
	return out
}

// filterDirs returns /cd <path> picker items for directory autocomplete.
// partial is whatever the user typed after "/cd ".
func filterDirs(workDir, partial string) []pickerItem {
	var baseDir, prefix string

	expandHome := func(p string) string {
		if strings.HasPrefix(p, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				return filepath.Join(home, p[2:])
			}
		}
		return p
	}

	partial = expandHome(partial)

	switch {
	case partial == "":
		baseDir = workDir
		prefix = ""
	case strings.HasSuffix(partial, string(filepath.Separator)):
		baseDir = filepath.Clean(partial)
		if !filepath.IsAbs(baseDir) {
			baseDir = filepath.Join(workDir, baseDir)
		}
		prefix = ""
	default:
		joined := partial
		if !filepath.IsAbs(partial) {
			joined = filepath.Join(workDir, partial)
		}
		baseDir = filepath.Dir(joined)
		prefix = filepath.Base(joined)
	}

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil
	}

	home, _ := os.UserHomeDir()

	// Always offer ".." to go up (unless already at root)
	var items []pickerItem
	if partial == "" && workDir != "/" {
		parent := filepath.Dir(workDir)
		displayParent := parent
		if home != "" && strings.HasPrefix(parent, home) {
			displayParent = "~" + parent[len(home):]
		}
		items = append(items, pickerItem{
			cmd:     "/cd ..",
			desc:    "↑ " + displayParent,
			hasArgs: false,
		})
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		// Hide dot-dirs unless user explicitly typed a dot
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(prefix, ".") {
			continue
		}
		if prefix != "" && !strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix)) {
			continue
		}
		fullPath := filepath.Join(baseDir, name)
		// Prefer relative path from workDir; fall back to ~/… or absolute
		displayPath := fullPath
		if rel, err := filepath.Rel(workDir, fullPath); err == nil && !strings.HasPrefix(rel, "..") {
			displayPath = rel
		} else if home != "" && strings.HasPrefix(fullPath, home) {
			displayPath = "~" + fullPath[len(home):]
		}
		items = append(items, pickerItem{
			cmd:     "/cd " + displayPath,
			desc:    "📁 " + name,
			hasArgs: false,
		})
	}
	return items
}

// pullMsg carries progress or completion back into the update loop.
type pullMsg struct {
	progress *backend.PullProgress // nil = done
	err      error
}

type chatMsg struct {
	content string
	err     error
}

type compactMsg struct {
	summary string
}

type checkpointMsg struct {
	name    string
	summary string
	err     error
}

type suggestMsg struct {
	content string
	err     error
}

type explainMsg struct {
	content string
	err     error
}

type licenseActivatedMsg struct {
	lic        *license.License
	instanceID string
	err        error
}

type licenseValidatedMsg struct {
	lic *license.License
	err error
}

func activateLicenseCmd(newKey, oldKey, oldInstanceID string) tea.Cmd {
	return func() tea.Msg {
		// Best-effort deactivate old key first to free the seat
		if oldKey != "" && oldInstanceID != "" {
			_ = license.Deactivate(oldKey, oldInstanceID)
		}
		lic, instanceID, err := license.Activate(newKey, license.InstanceName())
		return licenseActivatedMsg{lic: lic, instanceID: instanceID, err: err}
	}
}

func validateLicenseCmd(key, instanceID string) tea.Cmd {
	return func() tea.Msg {
		lic, err := license.Validate(key, instanceID)
		return licenseValidatedMsg{lic: lic, err: err}
	}
}

func backendConfigPath() string {
	if v := os.Getenv("BILLY_CONFIG"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "~/.localai/config.toml"
	}
	return filepath.Join(home, ".localai", "config.toml")
}

func configuredBackendURL(cfg *config.Config) string {
	configuredURL := strings.TrimSpace(cfg.Backend.URL)
	if configuredURL == "" && backend.NormalizeType(cfg.Backend.Type) == "ollama" {
		configuredURL = "http://localhost:11434"
	}
	return configuredURL
}

func backendReachable(rawURL string) bool {
	if strings.TrimSpace(rawURL) == "" {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
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

func welcomeContent(cfg *config.Config, b backend.Backend) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("  Billy 🐐  —  Backend: %s  ·  Model: %s\n", b.Name(), b.CurrentModel()))

	if backend.IsOllamaBackend(b) && !backendReachable(configuredBackendURL(cfg)) {
		sb.WriteString("  Ollama is not reachable yet.\n\n")
		if backend.ShouldAutoLaunchOllama(cfg) {
			sb.WriteString("  Start here:\n")
			sb.WriteString("  1. Install or start Ollama: https://ollama.com\n")
			sb.WriteString("  2. Pull a model: ollama pull qwen2.5-coder:14b\n")
			sb.WriteString("  3. Then run /backend reload or reopen Billy\n\n")
		} else {
			sb.WriteString("  Check your configured Ollama URL, then run /backend reload.\n\n")
		}
		sb.WriteString("  Commands: /help  ·  /backend  ·  /model\n\n")
		return dimStyle.Render(sb.String())
	}

	if backend.IsOllamaBackend(b) {
		sb.WriteString("  Try: explain this repository  ·  /model  ·  /pull qwen2.5-coder:14b\n\n")
	} else {
		sb.WriteString("  Try: explain this repository  ·  /backend  ·  /model\n\n")
	}
	return dimStyle.Render(sb.String())
}

func (m *ChatModel) persistCurrentModel(model string) error {
	if backend.IsOllamaBackend(m.backend) {
		m.cfg.Ollama.Model = model
	} else {
		m.cfg.Backend.Model = model
	}
	return config.Save(m.cfg)
}

func (m ChatModel) reloadBackendFromConfig() (ChatModel, error) {
	cfg, err := config.Load()
	if err != nil {
		return m, err
	}

	b, err := backend.NewFromConfig(cfg, m.lic)
	if err != nil {
		return m, err
	}

	m.cfg = cfg
	m.backend = b
	m.conversationID = ""
	m.history = nil
	m.tokenEstimate = 0
	return m, nil
}

func (m ChatModel) renderBackendStatus() string {
	configuredType := backend.NormalizeType(m.cfg.Backend.Type)
	if configuredType == "" {
		configuredType = "ollama"
	}

	configuredURL := configuredBackendURL(m.cfg)

	configuredModel := backend.ResolveModel(m.cfg)
	if configuredModel == "" {
		configuredModel = "(unset)"
	}

	ollamaReachable := false
	if configuredType == "ollama" {
		ollamaReachable = backendReachable(configuredURL)
	}

	var sb strings.Builder
	sb.WriteString("\nBackend status:\n")
	sb.WriteString(fmt.Sprintf("  Active:     %s\n", m.backend.Name()))
	sb.WriteString(fmt.Sprintf("  Configured: %s\n", configuredType))
	sb.WriteString(fmt.Sprintf("  URL:        %s\n", configuredURL))
	sb.WriteString(fmt.Sprintf("  Model:      %s\n", configuredModel))
	if configuredType == "ollama" {
		if ollamaReachable {
			sb.WriteString("  Reachable:   yes\n")
		} else {
			sb.WriteString("  Reachable:   no\n")
		}
	}
	if strings.TrimSpace(m.cfg.Backend.APIKey) != "" || os.Getenv("BILLY_API_KEY") != "" {
		sb.WriteString("  API key:    set\n")
	} else {
		sb.WriteString("  API key:    not set\n")
	}
	sb.WriteString("\nUsage:\n")
	sb.WriteString("  /backend          Show this status\n")
	sb.WriteString("  /backend reload   Reload ~/.localai/config.toml and rebuild the backend\n")
	sb.WriteString("\nConfig file:\n")
	sb.WriteString(fmt.Sprintf("  %s\n", backendConfigPath()))
	if configuredType == "ollama" && !ollamaReachable {
		sb.WriteString("\nNext steps:\n")
		if backend.ShouldAutoLaunchOllama(m.cfg) {
			sb.WriteString("  1. Install or start Ollama: https://ollama.com\n")
			sb.WriteString("  2. Pull a model: ollama pull qwen2.5-coder:14b\n")
			sb.WriteString("  3. Then run /backend reload\n")
		} else {
			sb.WriteString("  1. Verify backend.url points at your Ollama server\n")
			sb.WriteString("  2. Make sure that server is running and reachable\n")
			sb.WriteString("  3. Then run /backend reload\n")
		}
	}
	sb.WriteString("\nBilly currently ships fully unlocked. Custom OpenAI-compatible endpoints work in the open-core build too.\n")
	sb.WriteString("\n")
	return sb.String()
}

// estimateTokens gives a rough token count for the history (4 chars ≈ 1 token).
func estimateTokens(history []backend.Message) int {
	total := 0
	for _, m := range history {
		total += len(m.Content) / 4
	}
	return total
}

// ansiRegexp strips ANSI escape sequences for plain-text line searching.
var ansiRegexp = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiRegexp.ReplaceAllString(s, "") }

// collapsedOutput tracks a long command output that has been folded in the viewport.
type collapsedOutput struct {
	marker   string // unique placeholder embedded in m.content
	full     string // complete output text (always sent to AI)
	hidden   int    // number of lines hidden below the preview
	expanded bool   // whether the user has expanded it
	hintLine int    // line index in rendered viewport content (updated by render)
}

// ChatModel is the Bubble Tea model for the main chat interface.
type ChatModel struct {
	cfg                     *config.Config
	backend                 backend.Backend
	store                   *store.Store
	lic                     *license.License // nil = free tier
	msgCount                int              // messages sent this session (for free limit)
	conversationID          string
	history                 []backend.Message
	content                 string // raw accumulated content for the viewport
	viewport                viewport.Model
	textarea                textarea.Model
	spinner                 spinner.Model
	historyMode             bool
	historyList             list.Model
	width                   int
	height                  int
	waiting                 bool
	showPicker              bool
	pickerItems             []pickerItem
	pickerIdx               int
	activating              bool   // true while /activate key-entry prompt is shown
	pendingActivationKey    string // key being activated (for licenseActivatedMsg handler)
	pendingValidation       bool   // trigger background re-validation on Init
	pendingValidationKey    string
	pendingValidationInstID string
	shellPending            string            // shell command awaiting user permission
	shellAlways             map[string]bool   // session-level "always run" prefixes
	shellPickerIdx          int               // 0=Run once, 1=Always, 2=Cancel
	progressBar             progress.Model    // animated bar for /pull downloads
	isPulling               bool              // true while model pull in progress
	pullStatus              string            // current pull status string from Ollama
	pullModelName           string            // model name being pulled
	pendingCmdOutputs       []string          // shell outputs buffered for AI feedback
	collapsedOutputs        []collapsedOutput // folded long command outputs
	cmdQueue                []string          // AI-suggested commands pending permission
	agentMode               bool              // true = agentic (default), false = chat only
	autopilotMode           bool              // true = auto-run commands and keep iterating
	lastAutoCommandSig      string            // normalized signature of last successful autopilot batch
	lastAutoCommandSuccess  bool              // true if the last autopilot batch had no shell error
	teachMode               bool              // true = teach mode (show commands, don't run)
	tokenEstimate           int               // rough token count for current history
	compacted               bool              // true if history has been compacted
	workDir                 string            // current working directory for the session
}

// New creates a new ChatModel.
func New(cfg *config.Config, b backend.Backend, s *store.Store) ChatModel {
	ta := textarea.New()
	ta.Placeholder = defaultPromptPlaceholder
	ta.Focus()
	ta.SetWidth(80)
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	ta.CharLimit = 4096

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))

	welcome := welcomeContent(cfg, b)

	vp := viewport.New(80, 20)
	vp.SetContent(welcome)

	m := ChatModel{
		cfg:         cfg,
		backend:     b,
		store:       s,
		content:     welcome,
		viewport:    vp,
		textarea:    ta,
		spinner:     sp,
		shellAlways: make(map[string]bool),
		agentMode:   true, // agentic by default
		progressBar: progress.New(
			progress.WithGradient("#38bdf8", "#a855f7"),
			progress.WithWidth(60),
		),
	}

	if wd, err := os.Getwd(); err == nil {
		m.workDir = wd
	}

	return m
}

func (m ChatModel) Init() tea.Cmd {
	if m.pendingValidation {
		return tea.Batch(textarea.Blink, validateLicenseCmd(m.pendingValidationKey, m.pendingValidationInstID))
	}
	return textarea.Blink
}

func (m ChatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		taCmd tea.Cmd
		vpCmd tea.Cmd
		spCmd tea.Cmd
	)

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width - 4
		m.viewport.Height = msg.Height - 8
		m.textarea.SetWidth(msg.Width - 4)
		m.progressBar.Width = msg.Width - 12
		if m.historyMode {
			m.historyList.SetWidth(msg.Width - 4)
			m.historyList.SetHeight(msg.Height - 6)
		}
		m.render()
		return m, nil

	// ── History picker mode ──────────────────────────────────────────────
	case tea.KeyMsg:
		if m.historyMode {
			switch msg.Type {
			case tea.KeyEsc, tea.KeyCtrlC:
				m.historyMode = false
				return m, nil
			case tea.KeyEnter:
				if item, ok := m.historyList.SelectedItem().(historyItem); ok {
					return m.loadConversation(item.conv.ID)
				}
				return m, nil
			}
			var listCmd tea.Cmd
			m.historyList, listCmd = m.historyList.Update(msg)
			return m, listCmd
		}

		// ── /activate key-entry mode ─────────────────────────────────────────
		if m.activating {
			switch msg.Type {
			case tea.KeyEsc, tea.KeyCtrlC:
				m.activating = false
				m.textarea.Placeholder = defaultPromptPlaceholder
				m.textarea.Reset()
				m.append(dimStyle.Render("Activation cancelled.\n\n"))
				return m, nil
			case tea.KeyEnter:
				key := strings.TrimSpace(m.textarea.Value())
				m.activating = false
				m.textarea.Placeholder = defaultPromptPlaceholder
				m.textarea.Reset()
				if key == "" {
					m.append(errorStyle.Render("❌ No key entered. Use /activate to try again.\n\n"))
					return m, nil
				}
				// Read any existing activation so we can deactivate it first (frees old seat)
				var oldKey, oldInstID string
				if m.store != nil {
					if actBytes, err := m.store.GetEncrypted("ls_activation"); err == nil && len(actBytes) > 0 {
						if act, err := license.UnmarshalActivation(actBytes); err == nil {
							oldKey, oldInstID = act.Key, act.InstanceID
						}
					}
				}
				m.pendingActivationKey = key
				m.append(dimStyle.Render("🔄 Contacting activation server...\n\n"))
				return m, activateLicenseCmd(key, oldKey, oldInstID)
			}
			m.textarea, taCmd = m.textarea.Update(msg)
			return m, taCmd
		}

		// ── Shell permission picker ────────────────────────────────────────────
		if m.shellPending != "" {
			switch msg.Type {
			case tea.KeyUp:
				if m.shellPickerIdx > 0 {
					m.shellPickerIdx--
				}
				return m, nil
			case tea.KeyDown:
				if m.shellPickerIdx < 2 {
					m.shellPickerIdx++
				}
				return m, nil
			case tea.KeyEnter:
				pending := m.shellPending
				m.shellPending = ""
				switch m.shellPickerIdx {
				case 0: // Run once
					m = m.executeShell(pending)
				case 1: // Always this session
					prefix := strings.Fields(pending)[0]
					m.shellAlways[prefix] = true
					m = m.executeShell(pending)
				case 2: // Cancel
					m.append(dimStyle.Render("Skipped.\n\n"))
					m.cmdQueue = nil
					m.pendingCmdOutputs = nil
				}
				m.shellPickerIdx = 0
				if len(m.cmdQueue) > 0 {
					m = m.promptNextQueuedCmd()
					return m, nil
				}
				if len(m.pendingCmdOutputs) > 0 {
					m, cmd := m.flushCmdOutputs()
					return m, cmd
				}
				m.textarea.Placeholder = defaultPromptPlaceholder
				m.textarea.Reset()
				return m, nil
			case tea.KeyEsc:
				m.shellPending = ""
				m.shellPickerIdx = 0
				m.cmdQueue = nil
				m.pendingCmdOutputs = nil
				m.textarea.Placeholder = defaultPromptPlaceholder
				m.textarea.Reset()
				m.append(dimStyle.Render("Skipped.\n\n"))
				return m, nil
			}
			// Block all other keypresses while permission picker is active
			return m, nil
		}
		// Command picker navigation — intercepts keys when picker is visible
		if m.showPicker && len(m.pickerItems) > 0 {
			switch msg.Type {
			case tea.KeyUp:
				if m.pickerIdx > 0 {
					m.pickerIdx--
				}
				return m, nil
			case tea.KeyDown:
				if m.pickerIdx < len(m.pickerItems)-1 {
					m.pickerIdx++
				}
				return m, nil
			case tea.KeyEnter:
				selected := m.pickerItems[m.pickerIdx]
				if selected.hasArgs {
					m.textarea.SetValue(selected.cmd + " ")
				} else {
					m.textarea.SetValue(selected.cmd)
				}
				m.showPicker = false
				m.pickerIdx = 0
				if !selected.hasArgs {
					return m, func() tea.Msg { return tea.KeyMsg{Type: tea.KeyEnter} }
				}
				return m, nil
			case tea.KeyEsc:
				m.showPicker = false
				m.pickerIdx = 0
				return m, nil
			}
		}
		switch msg.Type {
		case tea.KeyCtrlX:
			// Expand the most recently collapsed command output
			for i := len(m.collapsedOutputs) - 1; i >= 0; i-- {
				if !m.collapsedOutputs[i].expanded {
					m.collapsedOutputs[i].expanded = true
					m.render()
					return m, nil
				}
			}
			return m, nil
		case tea.KeyCtrlD, tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyEnter:
			if m.waiting {
				return m, nil
			}
			input := strings.TrimSpace(m.textarea.Value())
			if input == "" {
				return m, nil
			}
			m.textarea.Reset()
			m.showPicker = false

			if strings.HasPrefix(input, "/") {
				return m.handleCommand(input)
			}

			m.msgCount++

			// Natural language memory detection
			if fact, ok := memory.DetectAndExtract(input); ok {
				if m.store != nil {
					if err := m.store.SaveMemory(uuid.New().String(), fact); err == nil {
						m.append(assistantStyle.Render("Billy >") + " " +
							dimStyle.Render(fmt.Sprintf("Got it! I'll remember: \"%s\"\n\n", fact)))
					} else {
						m.append(errorStyle.Render("Couldn't save memory: "+err.Error()) + "\n\n")
					}
				} else {
					m.append(dimStyle.Render("(Memory not available — no storage)\n\n"))
				}
				return m, nil
			}

			// Ensure a conversation exists in the store
			if m.store != nil && m.conversationID == "" {
				m.conversationID = uuid.New().String()
				title := input
				if len(title) > 60 {
					title = title[:60] + "…"
				}
				_ = m.store.CreateConversation(m.conversationID, title, m.backend.CurrentModel())
			}

			m.lastAutoCommandSig = ""
			m.lastAutoCommandSuccess = false
			m.history = append(m.history, backend.Message{Role: "user", Content: input})
			m.tokenEstimate = estimateTokens(m.history)
			m.append(userStyle.Render("You >") + " " + input + "\n\n")

			// Persist user message
			if m.store != nil && m.conversationID != "" {
				_ = m.store.AddMessage(uuid.New().String(), m.conversationID, "user", input)
			}

			m.waiting = true
			return m, tea.Batch(m.sendChat(), m.spinner.Tick)
		}

	case progress.FrameMsg:
		if m.isPulling {
			pm, cmd := m.progressBar.Update(msg)
			m.progressBar = pm.(progress.Model)
			return m, cmd
		}
		return m, nil

	case pullMsg:
		if msg.err != nil {
			m.isPulling = false
			m.waiting = false
			m.append(errorStyle.Render("Pull failed: "+msg.err.Error()) + "\n\n")
		} else if msg.progress == nil {
			// Pull complete
			m.isPulling = false
			m.waiting = false
			m.append(dimStyle.Render(fmt.Sprintf("✅ Downloaded %s successfully!\n\n", m.pullModelName)))
		} else {
			// Progress update — drive the animated bar
			m.isPulling = true
			m.pullStatus = msg.progress.Status
			if msg.progress.Total > 0 {
				pct := float64(msg.progress.Completed) / float64(msg.progress.Total)
				return m, m.progressBar.SetPercent(pct)
			}
		}
		return m, m.spinner.Tick

	case chatMsg:
		m.waiting = false
		if msg.err != nil {
			m.append(errorStyle.Render("Error: "+msg.err.Error()) + "\n\n")
		} else {
			m.history = append(m.history, backend.Message{Role: "assistant", Content: msg.content})
			m.tokenEstimate = estimateTokens(m.history)
			m.append(assistantStyle.Render("Billy >") + " " + msg.content + "\n\n")

			// Persist assistant message
			if m.store != nil && m.conversationID != "" {
				_ = m.store.AddMessage(uuid.New().String(), m.conversationID, "assistant", msg.content)
			}

			// In agent mode, detect shell commands and queue them for permission
			if m.agentMode {
				cmds := extractShellCommands(msg.content)
				if len(cmds) > 0 {
					sig := normalizeCommandBatch(cmds)
					if m.autopilotMode && m.lastAutoCommandSuccess && sig != "" && sig == m.lastAutoCommandSig {
						m.append(dimStyle.Render("🛑 Autopilot stopped to avoid repeating the same successful command batch.\nBilly believes the task is already complete.\n\n"))
						m.lastAutoCommandSig = ""
						m.lastAutoCommandSuccess = false
						return m, nil
					}
					m.cmdQueue = append(m.cmdQueue, cmds...)
					m = m.promptNextQueuedCmd()
					// If shellAlways drained the whole queue, flush outputs now
					if m.shellPending == "" && len(m.cmdQueue) == 0 && len(m.pendingCmdOutputs) > 0 {
						m, flushCmd := m.flushCmdOutputs()
						return m, flushCmd
					}
				}
			} else if m.teachMode {
				cmds := extractShellCommands(msg.content)
				if len(cmds) > 0 {
					for _, cmd := range cmds {
						box := lipgloss.NewStyle().
							Border(lipgloss.RoundedBorder()).
							BorderForeground(lipgloss.Color("#4ade80")).
							Padding(0, 1).
							Render("📝 Type this yourself:\n  " + cmd)
						m.append(box + "\n\n")
					}
				}
			}
		}
		return m, nil

	case spinner.TickMsg:
		if m.waiting || m.isPulling {
			m.spinner, spCmd = m.spinner.Update(msg)
			return m, spCmd
		}
		return m, nil

	case tea.MouseMsg:
		// Left-click inside the viewport to expand a collapsed output block
		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
			// Viewport content starts at Y=1 (Y=0 is the top border)
			if msg.Y >= 1 && msg.Y <= m.viewport.Height {
				clickedLine := (msg.Y - 1) + m.viewport.YOffset
				for i := range m.collapsedOutputs {
					if !m.collapsedOutputs[i].expanded && m.collapsedOutputs[i].hintLine == clickedLine {
						m.collapsedOutputs[i].expanded = true
						m.render()
						return m, nil
					}
				}
			}
		}
		// Forward to viewport for mouse-wheel scrolling
		m.viewport, vpCmd = m.viewport.Update(msg)
		return m, vpCmd

	case compactMsg:
		m.waiting = false
		keep := m.history
		if len(keep) > 6 {
			keep = keep[len(keep)-6:]
		}
		m.history = append([]backend.Message{
			{Role: "system", Content: "Previous conversation summary: " + msg.summary},
		}, keep...)
		m.compacted = true
		m.tokenEstimate = estimateTokens(m.history)
		m.append(dimStyle.Render(fmt.Sprintf("✅ Compacted! Summary:\n\n%s\n\n── Continuing from here ──\n\n", msg.summary)))
		return m, nil

	case checkpointMsg:
		m.waiting = false
		if msg.err != nil {
			m.append(errorStyle.Render("Checkpoint failed: " + msg.err.Error() + "\n\n"))
		} else {
			m.append(dimStyle.Render(fmt.Sprintf("✅ Checkpoint '%s' saved!\n\nSummary: %s\n\n", msg.name, msg.summary)))
		}
		return m, nil

	case suggestMsg:
		m.waiting = false
		if msg.err != nil {
			m.append(errorStyle.Render("❌ Suggest failed: " + msg.err.Error() + "\n\n"))
		} else {
			m.append(assistantStyle.Render("Billy >") + " " + wordwrap.String(msg.content, m.viewport.Width-4) + "\n\n")
		}
		return m, nil

	case explainMsg:
		m.waiting = false
		if msg.err != nil {
			m.append(errorStyle.Render("❌ Explain failed: " + msg.err.Error() + "\n\n"))
		} else {
			m.append(assistantStyle.Render("Billy >") + " " + wordwrap.String(msg.content, m.viewport.Width-4) + "\n\n")
		}
		return m, nil

	case licenseActivatedMsg:
		if msg.err != nil {
			m.append(errorStyle.Render("❌ Activation failed: " + msg.err.Error() + "\n\n"))
			return m, nil
		}
		act := license.Activation{
			Key:         m.pendingActivationKey,
			InstanceID:  msg.instanceID,
			Tier:        msg.lic.Tier,
			Seats:       msg.lic.Seats,
			Email:       msg.lic.Email,
			ValidatedAt: time.Now(),
		}
		if actBytes, err := act.Marshal(); err == nil && m.store != nil {
			_ = m.store.SetEncrypted("ls_activation", actBytes)
		}
		m.lic = msg.lic
		seatsNote := ""
		if msg.lic.Seats > 0 {
			seatsNote = fmt.Sprintf(" (%d seats)", msg.lic.Seats)
		}
		m.append(dimStyle.Render(fmt.Sprintf(
			"✅ License activated! Welcome to %s tier%s 🎉\nActivation stored securely on this machine.\n\n",
			strings.ToUpper(string(msg.lic.EffectiveTier())), seatsNote,
		)))
		return m, nil

	case licenseValidatedMsg:
		if msg.err != nil {
			m.append(errorStyle.Render("⚠️  License re-validation failed: " + msg.err.Error() + "\n\n"))
			return m, nil
		}
		// Refresh cached activation timestamp
		if m.store != nil {
			if actBytes, err := m.store.GetEncrypted("ls_activation"); err == nil && len(actBytes) > 0 {
				if act, err := license.UnmarshalActivation(actBytes); err == nil {
					act.ValidatedAt = time.Now()
					act.Tier = msg.lic.Tier
					if newBytes, err := act.Marshal(); err == nil {
						_ = m.store.SetEncrypted("ls_activation", newBytes)
					}
				}
			}
		}
		m.lic = msg.lic
		return m, nil
	}

	m.textarea, taCmd = m.textarea.Update(msg)
	m.viewport, vpCmd = m.viewport.Update(msg)

	// Update command picker visibility based on current input.
	// Only reset pickerIdx when the filtered list actually changes (i.e. the
	// user typed a new character). Blink ticks and other non-key messages must
	// NOT reset the selection — that was causing the picker to jump to the top.
	val := m.textarea.Value()
	if strings.HasPrefix(val, "/cd ") {
		// Directory autocomplete mode
		partial := strings.TrimPrefix(val, "/cd ")
		newItems := filterDirs(m.workDir, partial)
		listChanged := len(newItems) != len(m.pickerItems) ||
			(len(newItems) > 0 && len(m.pickerItems) > 0 && newItems[0].cmd != m.pickerItems[0].cmd)
		if listChanged {
			m.pickerIdx = 0
		}
		m.pickerItems = newItems
		m.showPicker = len(newItems) > 0
	} else if strings.HasPrefix(val, "/") && !strings.Contains(val, " ") {
		newItems := filterCommands(val)
		listChanged := len(newItems) != len(m.pickerItems) ||
			(len(newItems) > 0 && len(m.pickerItems) > 0 && newItems[0].cmd != m.pickerItems[0].cmd)
		if listChanged {
			m.pickerIdx = 0
		}
		m.pickerItems = newItems
		m.showPicker = len(newItems) > 0
	} else {
		m.showPicker = false
		m.pickerItems = nil
		m.pickerIdx = 0
	}

	return m, tea.Batch(taCmd, vpCmd)
}

func (m ChatModel) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	// History picker overlay
	if m.historyMode {
		return borderStyle.Width(m.width - 2).Render(m.historyList.View())
	}

	var status string
	if m.isPulling {
		status = m.spinner.View() + dimStyle.Render(fmt.Sprintf(" Downloading %s · %s", m.pullModelName, m.pullStatus))
	} else if m.waiting {
		status = m.spinner.View() + dimStyle.Render(" Billy is thinking...")
	} else {
		badge := licenseBadge(m.lic)
		var modeBadge string
		switch {
		case m.teachMode:
			modeBadge = lipgloss.NewStyle().Foreground(lipgloss.Color("#4ade80")).Bold(true).Render("[TEACH]")
		case m.autopilotMode:
			modeBadge = lipgloss.NewStyle().Foreground(lipgloss.Color("#f59e0b")).Bold(true).Render("[AUTO]")
		case m.agentMode:
			modeBadge = lipgloss.NewStyle().Foreground(lipgloss.Color("#38bdf8")).Bold(true).Render("[AGENT]")
		default:
			modeBadge = dimStyle.Render("[CHAT]")
		}
		pwdBadge := dimStyle.Render(abbreviatePath(m.workDir))
		status = dimStyle.Render(fmt.Sprintf(" %s · %s  — PgUp/PgDn to scroll", m.backend.Name(), m.backend.CurrentModel())) + " " + modeBadge + " " + badge + "  " + pwdBadge
		if m.tokenEstimate > 0 {
			status += " " + renderTokenBar(m.tokenEstimate)
		}
	}

	parts := []string{
		borderStyle.Width(m.width - 2).Render(m.viewport.View()),
		status,
	}
	if m.isPulling {
		pullOverlay := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#38bdf8")).
			Padding(0, 1).
			Render(dimStyle.Render("⬇  "+m.pullModelName) + "\n" + m.progressBar.View())
		parts = append(parts, pullOverlay)
	} else if m.shellPending != "" {
		if picker := m.renderShellPicker(); picker != "" {
			parts = append(parts, picker)
		}
	} else if picker := m.renderPicker(); picker != "" {
		parts = append(parts, picker)
	}
	parts = append(parts, borderStyle.Width(m.width-2).Render(m.textarea.View()))
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// licenseBadge returns a styled tier badge for the status bar.
func licenseBadge(_ *license.License) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#4ade80")).Bold(true).Render("[OPEN]")
}

// renderTokenBar renders a compact coloured context-fill bar for the status line.
func renderTokenBar(estimate int) string {
	const maxCtx = 4096
	const barWidth = 10
	pct := float64(estimate) / maxCtx
	if pct > 1.0 {
		pct = 1.0
	}
	color := "#22c55e"
	if pct > 0.9 {
		color = "#ef4444"
	} else if pct > 0.75 {
		color = "#f59e0b"
	}
	filled := int(pct * barWidth)
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(strings.Repeat("█", filled)) +
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(strings.Repeat("░", barWidth-filled)) +
		" " + dimStyle.Render(fmt.Sprintf("%dk", estimate/1000))
}

// abbreviatePath replaces the home directory with ~ and keeps the last 2 segments
// if the full path is long, so the status bar stays compact.
func abbreviatePath(p string) string {
	if p == "" {
		return "~"
	}
	if home, err := os.UserHomeDir(); err == nil {
		if p == home {
			return "~"
		}
		if strings.HasPrefix(p, home+string(filepath.Separator)) {
			p = "~" + p[len(home):]
		}
	}
	// Keep last 3 path segments if still long
	parts := strings.Split(filepath.ToSlash(p), "/")
	if len(parts) > 4 {
		parts = append([]string{"…"}, parts[len(parts)-3:]...)
		return strings.Join(parts, "/")
	}
	return p
}

func (m ChatModel) renderPicker() string {
	if !m.showPicker || len(m.pickerItems) == 0 {
		return ""
	}

	const maxVisible = 7
	total := len(m.pickerItems)

	// Scroll window: keep selected item visible
	start := 0
	if m.pickerIdx >= maxVisible {
		start = m.pickerIdx - maxVisible + 1
	}
	end := start + maxVisible
	if end > total {
		end = total
	}

	selectedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("12")).
		Bold(true)
	dimRowStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8"))
	cmdStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("7"))

	var rows []string

	// Scroll indicator at top
	if start > 0 {
		rows = append(rows, dimRowStyle.Render(fmt.Sprintf("  ↑ %d more", start)))
	}

	for i := start; i < end; i++ {
		item := m.pickerItems[i]
		if i == m.pickerIdx {
			cursor := "▶ "
			cmdPart := selectedStyle.Render(fmt.Sprintf("%-12s", item.cmd))
			descPart := lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Render(item.desc)
			rows = append(rows, fmt.Sprintf("%s%s  %s", cursor, cmdPart, descPart))
		} else {
			cursor := "  "
			cmdPart := cmdStyle.Render(fmt.Sprintf("%-12s", item.cmd))
			descPart := dimRowStyle.Render(item.desc)
			rows = append(rows, fmt.Sprintf("%s%s  %s", cursor, cmdPart, descPart))
		}
	}

	// Scroll indicator at bottom
	remaining := total - end
	if remaining > 0 {
		rows = append(rows, dimRowStyle.Render(fmt.Sprintf("  ↓ %d more", remaining)))
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("12")).
		Padding(0, 1).
		Render(strings.Join(rows, "\n"))
}

// renderShellPicker renders the arrow-key permission picker for agentic shell commands.
func (m ChatModel) renderShellPicker() string {
	if m.shellPending == "" {
		return ""
	}
	cmdHighlight := lipgloss.NewStyle().Foreground(lipgloss.Color("#38bdf8")).Bold(true)
	selectedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#f59e0b")).Bold(true)
	dimRow := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	options := [3]string{"Run once", "Always this session", "Cancel"}
	var rows []string
	rows = append(rows, cmdHighlight.Render("⚡ "+m.shellPending))
	rows = append(rows, "")
	for i, opt := range options {
		if i == m.shellPickerIdx {
			rows = append(rows, selectedStyle.Render("▶  "+opt))
		} else {
			rows = append(rows, dimRow.Render("   "+opt))
		}
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#f59e0b")).
		Padding(0, 1).
		Render(strings.Join(rows, "\n"))
}

// append adds raw text to the content buffer then re-renders the viewport.
func (m *ChatModel) append(text string) {
	// lipgloss.Render does not emit a trailing newline on the last line, so
	// we must ensure content always ends with \n before appending new text.
	if len(m.content) > 0 && !strings.HasSuffix(m.content, "\n") {
		m.content += "\n"
	}
	m.content += text
	m.render()
}

// appendCmdOutput appends command output to the viewport, collapsing it when it
// exceeds the preview threshold. The full record is always sent to AI context.
func (m *ChatModel) appendCmdOutput(record string, isError bool) {
	const threshold = 15
	const preview = 10
	lines := strings.Split(strings.TrimRight(record, "\n"), "\n")
	if len(lines) > threshold {
		previewText := strings.Join(lines[:preview], "\n")
		marker := fmt.Sprintf("[[BILLY_COLLAPSE_%d]]", time.Now().UnixNano())
		m.collapsedOutputs = append(m.collapsedOutputs, collapsedOutput{
			marker: marker,
			full:   record,
			hidden: len(lines) - preview,
		})
		if isError {
			m.append(errorStyle.Render(previewText) + "\n" + marker + "\n\n")
		} else {
			m.append(dimStyle.Render(previewText) + "\n" + marker + "\n\n")
		}
	} else if isError {
		m.append(errorStyle.Render(record) + "\n\n")
	} else {
		m.append(dimStyle.Render(record) + "\n")
	}
}

// render word-wraps m.content to the current viewport width and scrolls to bottom.
// Collapse markers are substituted before wrapping; hint line positions are
// recorded so mouse clicks can identify which block to expand.
func (m *ChatModel) render() {
	width := m.viewport.Width - 2
	if width <= 0 {
		width = 78
	}
	content := m.content
	for i := range m.collapsedOutputs {
		co := &m.collapsedOutputs[i]
		if co.expanded {
			content = strings.Replace(content, co.marker, dimStyle.Render(co.full), 1)
		} else {
			hint := dimStyle.Render(fmt.Sprintf("  ╰─ [+] %d lines hidden · click or Ctrl+X to expand", co.hidden))
			content = strings.Replace(content, co.marker, hint, 1)
		}
	}
	wrapped := wordwrap.String(content, width)

	m.viewport.SetContent(wrapped)
	// Record viewport line positions of each hint for mouse-click targeting
	lines := strings.Split(wrapped, "\n")
	for i := range m.collapsedOutputs {
		if !m.collapsedOutputs[i].expanded {
			search := fmt.Sprintf("%d lines hidden", m.collapsedOutputs[i].hidden)
			for lineIdx, line := range lines {
				if strings.Contains(stripANSI(line), search) {
					m.collapsedOutputs[i].hintLine = lineIdx
					break
				}
			}
		}
	}
	m.viewport.GotoBottom()
}

// loadConversation restores a past conversation into the chat.
func (m ChatModel) loadConversation(id string) (ChatModel, tea.Cmd) {
	m.historyMode = false

	if m.store == nil {
		m.append(errorStyle.Render("No storage available.\n\n"))
		return m, nil
	}

	msgs, err := m.store.GetMessages(id)
	if err != nil {
		m.append(errorStyle.Render("Failed to load conversation: "+err.Error()) + "\n\n")
		return m, nil
	}

	// Rebuild history and viewport content
	m.conversationID = id
	m.history = nil
	m.content = dimStyle.Render(fmt.Sprintf(
		"  Billy 🐐  —  Resumed conversation  ·  Model: %s\n\n",
		m.backend.CurrentModel(),
	)) + "\n"

	for _, msg := range msgs {
		m.history = append(m.history, backend.Message{Role: msg.Role, Content: msg.Content})
		switch msg.Role {
		case "user":
			m.content += userStyle.Render("You >") + " " + msg.Content + "\n\n"
		case "assistant":
			m.content += assistantStyle.Render("Billy >") + " " + msg.Content + "\n\n"
		}
	}

	m.render()
	m.append(dimStyle.Render("── Conversation loaded. Continue from here. ──\n\n"))
	return m, nil
}

// sendChat fires off a chat request and returns the result as a chatMsg.
// It prepends a system prompt built from memories before sending history.
func (m ChatModel) sendChat() tea.Cmd {
	// Build system prompt from memories
	var memTexts []string
	if m.store != nil {
		if mems, err := m.store.ListMemories(); err == nil {
			for _, mem := range mems {
				memTexts = append(memTexts, mem.Content)
			}
		}
	}
	systemPrompt := memory.BuildSystemPrompt(memTexts)
	if m.agentMode {
		systemPrompt = agentSystemPrompt + "\n\n" + systemPrompt
	} else if m.teachMode {
		systemPrompt = teachSystemPrompt + "\n\n" + systemPrompt
	}

	// Prepend system message to history for this request
	fullHistory := make([]backend.Message, 0, len(m.history)+1)
	fullHistory = append(fullHistory, backend.Message{Role: "system", Content: systemPrompt})
	fullHistory = append(fullHistory, m.history...)

	opts := backend.ChatOptions{
		Temperature: m.cfg.Ollama.Temperature,
		NumPredict:  m.cfg.Ollama.NumPredict,
	}
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), backendRequestTimeout)
		defer cancel()
		content, err := b.Chat(ctx, fullHistory, opts)
		return chatMsg{content: content, err: err}
	}
}

// handleCommand routes slash commands.
func (m ChatModel) handleCommand(input string) (ChatModel, tea.Cmd) {
	parts := strings.Fields(input)
	cmd := parts[0]

	switch cmd {
	case "/activate":
		m.textarea.Reset()
		m.append(dimStyle.Render("Billy currently ships fully unlocked. No license key is required right now.\nFuture supporter or convenience tiers may return later, but the core tool is open.\n\n"))
		return m, nil

	case "/deactivate":
		m.append(dimStyle.Render("No active license is required right now. Billy is running in open-core mode.\n\n"))
		m.textarea.Reset()
		return m, nil

	case "/license":
		m.append(dimStyle.Render("🔓 Billy access: OPEN\n\nIncluded right now:\n• Local Ollama backend\n• Custom OpenAI-compatible endpoints\n• Local history and memory\n• Agentic, autopilot, and admin controls\n• No license key required\n\nIf Billy helps you, support the project at https://billysh.online through setup help, GitHub Sponsors, or Buy Me a Coffee.\nFuture supporter bundles may return later, but the core tool stays open.\n\n"))
		m.textarea.Reset()
		return m, nil

	case "/backend":
		if len(parts) == 1 {
			m.append(dimStyle.Render(m.renderBackendStatus()))
			m.textarea.Reset()
			return m, nil
		}
		if parts[1] != "reload" {
			m.append(errorStyle.Render("Usage: /backend  or  /backend reload\n\n"))
			m.textarea.Reset()
			return m, nil
		}

		reloaded, err := m.reloadBackendFromConfig()
		if err != nil {
			m.append(errorStyle.Render("Could not reload backend: " + err.Error() + "\n\n"))
			m.textarea.Reset()
			return m, nil
		}
		m = reloaded
		m.append(dimStyle.Render(fmt.Sprintf("✅ Reloaded backend: %s · %s\nConversation context reset to avoid cross-provider mixups.\n\n", m.backend.Name(), m.backend.CurrentModel())))
		m.textarea.Reset()
		return m, nil

	case "/run":
		// Shell command execution with permission prompt
		if len(parts) < 2 {
			m.append(dimStyle.Render("Usage: /run <shell command>\nExample: /run ls -la\n\n"))
			m.textarea.Reset()
			return m, nil
		}
		shellCmd := strings.Join(parts[1:], " ")
		return m.promptShellRun(shellCmd), nil

	case "/mode":
		if len(parts) < 2 {
			modeStr := "AGENT"
			if m.autopilotMode {
				modeStr = "AUTOPILOT"
			} else if m.teachMode {
				modeStr = "TEACH"
			} else if !m.agentMode {
				modeStr = "CHAT"
			}
			m.append(dimStyle.Render(fmt.Sprintf("Current mode: %s\n\n  /mode agent      — Billy detects commands and asks before running them\n  /mode autopilot  — Billy auto-runs suggested commands and keeps iterating\n  /mode chat       — conversation only, no command execution\n  /mode teach      — Billy guides step by step, shows commands for you to type\n\n", modeStr)))
		} else {
			// Check admin lock before allowing mode switch
			if m.store != nil {
				if cfgBytes, err := m.store.GetEncrypted("admin_config"); err == nil && len(cfgBytes) > 0 {
					var ac adminConfig
					if json.Unmarshal(cfgBytes, &ac) == nil && ac.Locked {
						m.append(errorStyle.Render("⛔ Mode is locked by admin. Contact your administrator.\n\n"))
						m.textarea.Reset()
						return m, nil
					}
				}
			}
			switch parts[1] {
			case "agent":
				m.agentMode = true
				m.autopilotMode = false
				delete(m.shellAlways, "*")
				m.teachMode = false
				m.append(dimStyle.Render("✅ Switched to AGENT mode.\nBilly will detect commands in responses and ask to run them.\n\n"))
			case "autopilot":
				m.agentMode = true
				m.autopilotMode = true
				m.teachMode = false
				m.shellAlways["*"] = true
				m.append(dimStyle.Render("✅ Switched to AUTOPILOT mode.\nBilly will auto-run suggested commands and keep iterating until the task settles.\n\n"))
			case "chat":
				m.agentMode = false
				m.autopilotMode = false
				delete(m.shellAlways, "*")
				m.teachMode = false
				m.cmdQueue = nil
				m.append(dimStyle.Render("✅ Switched to CHAT mode.\nBilly will answer questions only — no command execution.\n\n"))
			case "teach":
				m.teachMode = true
				m.agentMode = false
				m.autopilotMode = false
				delete(m.shellAlways, "*")
				m.cmdQueue = nil
				m.append(dimStyle.Render("✅ Switched to TEACH mode.\nBilly will guide you step by step. Shell commands are shown for you to type yourself.\n\n"))
			default:
				m.append(errorStyle.Render("Unknown mode. Use: /mode agent  or  /mode autopilot  or  /mode chat  or  /mode teach\n\n"))
			}
		}
		m.textarea.Reset()
		return m, nil

	case "/yolo":
		if m.store != nil {
			if cfgBytes, err := m.store.GetEncrypted("admin_config"); err == nil && len(cfgBytes) > 0 {
				var ac adminConfig
				if json.Unmarshal(cfgBytes, &ac) == nil && ac.Locked {
					m.append(errorStyle.Render("⛔ Mode is locked by admin. Contact your administrator.\n\n"))
					m.textarea.Reset()
					return m, nil
				}
			}
		}
		if m.autopilotMode {
			m.autopilotMode = false
			delete(m.shellAlways, "*")
			if !m.teachMode {
				m.agentMode = true
			}
			m.append(dimStyle.Render("🛑 YOLO disabled.\nBilly will ask before running commands again.\n\n"))
		} else {
			m.autopilotMode = true
			m.agentMode = true
			m.teachMode = false
			m.shellAlways["*"] = true
			m.append(dimStyle.Render("🚀 YOLO enabled.\nBilly will auto-run suggested commands for the rest of this session.\nUse /yolo again to turn it off.\n\n"))
		}
		m.textarea.Reset()
		return m, nil

	case "/help":
		modeStr := "AGENT (default)"
		if m.autopilotMode {
			modeStr = "AUTOPILOT"
		} else if m.teachMode {
			modeStr = "TEACH"
		} else if !m.agentMode {
			modeStr = "CHAT"
		}
		m.append(dimStyle.Render(fmt.Sprintf(`
Commands:
  /help              Show this help
  /backend [reload]  Show backend status or reload backend config
  /license           Show Billy access + support info
  /mode [agent|autopilot|chat|teach] Switch mode (current: %s)
  /model             List installed models (active model highlighted)
  /model <name>      Switch to a different model
  /pull <name>       Download a new model from Ollama (local backend only)
  /models            Alias for /model
  /yolo              Toggle auto-approve for all commands this session
  /memory            List everything Billy remembers about you
  /memory forget <id> Delete a specific memory
  /memory clear      Wipe all memories
  /run <cmd>         Run a shell command (asks for permission first)
  /save              Save this conversation
  /history           Browse past conversations (arrow keys + Enter to load)
  /resume <id>       Jump directly to a conversation by ID
  /compact           Summarize and compress conversation context
  /session           Save a session checkpoint (with AI summary)
  /session list      List all saved checkpoints
  /session load <n>  Restore a checkpoint by name
  /clear             Clear conversation history
  /quit, /exit       Exit Billy

Teaching mode:
  /teach             Shortcut for /mode teach
  /hint              Ask Billy for a more specific hint (teach mode)
  /admin             Admin controls: PIN, mode lock, curriculum

Filesystem:
  /pwd               Print current working directory
  /cd <path>         Change directory (↑↓ autocomplete as you type)
  /ls [path]         List files in directory
  /git               Show git branch, status, and recent commits

AI shell tools (like gh copilot):
  /suggest <task>    Suggest a shell command for a natural language task
  /explain <cmd>     Explain what a shell command does

Agent mode:
  When Billy suggests a command, a permission prompt appears.
  Press Enter or y=yes  a=always this session  n/s=skip

Autopilot mode:
  Billy auto-runs suggested commands and feeds output back into the model.
  Use /mode autopilot or /yolo to enable it.

Natural language memory:
  "Remember that I prefer Go over Python"
  "Note that my name is Jonathan"
  "Don't forget I'm building Billy"

Keyboard:
  PgUp / PgDn        Scroll conversation
  Ctrl+D / Ctrl+C    Quit

Popular models to pull:
  qwen2.5-coder:14b · qwen2.5-coder:7b · llama3 · codellama · phi3 · gemma · mistral
  Full list: https://ollama.com/library

`, modeStr)))

	case "/models":
		// Alias for /model with no args
		return m.handleCommand("/model")

	case "/teach":
		// Shortcut for /mode teach
		return m.handleCommand("/mode teach")

	case "/hint":
		hintMsg := backend.Message{Role: "user", Content: "Give me a more specific hint for what I should do next. Guide me step by step without giving me the complete answer."}
		m.waiting = true
		m.textarea.Reset()
		return m, tea.Batch(m.sendHintCmd(hintMsg), m.spinner.Tick)

	case "/admin":
		if len(parts) < 2 {
			m.append(dimStyle.Render("Admin commands:\n  /admin setup <pin>         — Set up admin PIN\n  /admin lock                — Lock mode to TEACH\n  /admin unlock              — Remove mode lock\n  /admin curriculum <text>   — Set curriculum context\n  /admin status              — Show admin config\n\n"))
			m.textarea.Reset()
			return m, nil
		}
		return m.handleAdminCommand(parts[1:])

	case "/memory":
		if m.store == nil {
			m.append(errorStyle.Render("Memory not available (no storage).\n\n"))
			break
		}
		subCmd := ""
		if len(parts) > 1 {
			subCmd = parts[1]
		}
		switch subCmd {
		case "forget":
			if len(parts) < 3 {
				m.append(errorStyle.Render("Usage: /memory forget <id>\n\n"))
			} else {
				ok, err := m.store.ForgetMemory(parts[2])
				if err != nil {
					m.append(errorStyle.Render("Error: "+err.Error()) + "\n\n")
				} else if !ok {
					m.append(dimStyle.Render(fmt.Sprintf("No memory found with id starting with '%s'\n\n", parts[2])))
				} else {
					m.append(dimStyle.Render("Memory forgotten.\n\n"))
				}
			}
		case "clear":
			if err := m.store.ClearMemories(); err != nil {
				m.append(errorStyle.Render("Error: "+err.Error()) + "\n\n")
			} else {
				m.append(dimStyle.Render("All memories cleared.\n\n"))
			}
		default:
			// List memories
			mems, err := m.store.ListMemories()
			if err != nil {
				m.append(errorStyle.Render("Error loading memories: "+err.Error()) + "\n\n")
			} else if len(mems) == 0 {
				m.append(dimStyle.Render("No memories yet. Tell me things like:\n  \"Remember that I prefer Go over Python\"\n\n"))
			} else {
				var sb strings.Builder
				sb.WriteString(fmt.Sprintf("\n🧠 Billy remembers %d thing(s) about you:\n\n", len(mems)))
				for _, mem := range mems {
					sb.WriteString(fmt.Sprintf("  [%s]  %s\n", mem.ID[:8], mem.Content))
				}
				sb.WriteString("\nUse /memory forget <id> to remove one.\n\n")
				m.append(dimStyle.Render(sb.String()))
			}
		}

	case "/model":
		if len(parts) < 2 {
			// No argument — list available models
			models, err := m.backend.ListModels(context.Background())
			if err != nil {
				m.append(errorStyle.Render("Error listing models: "+err.Error()) + "\n\n")
			} else if len(models) == 0 {
				if backend.IsOllamaBackend(m.backend) {
					m.append(dimStyle.Render("No models found yet.\nTry:\n  /pull qwen2.5-coder:14b\n  /pull llama3.2\n\n"))
				} else {
					m.append(dimStyle.Render("No models were returned by the current backend.\nSet backend.model manually if your provider does not expose /models.\n\n"))
				}
			} else {
				var sb strings.Builder
				sb.WriteString("\nAvailable models (use /model <name> to switch):\n")
				for _, mo := range models {
					active := "  "
					if mo.Name == m.backend.CurrentModel() {
						active = "▶ "
					}
					sb.WriteString(fmt.Sprintf("  %s%-32s %s\n", active, mo.Name, dimStyle.Render(mo.Size)))
				}
				if backend.IsOllamaBackend(m.backend) {
					sb.WriteString("\n  Use /pull <name> to download a new model.\n\n")
				} else {
					sb.WriteString("\n  This backend only switches between models it already exposes.\n\n")
				}
				m.append(dimStyle.Render(sb.String()))
			}
		} else {
			modelName := parts[1]
			m.backend.SetModel(modelName)
			m.conversationID = ""
			m.history = nil
			m.tokenEstimate = 0
			if err := m.persistCurrentModel(modelName); err != nil {
				m.append(errorStyle.Render("Switched model, but could not save config: " + err.Error() + "\n\n"))
				return m, nil
			}
			m.append(dimStyle.Render(fmt.Sprintf("Switched to model: %s\n\n", modelName)))
		}

	case "/pull":
		if !backend.IsOllamaBackend(m.backend) {
			m.append(errorStyle.Render("Model downloads are only available with the local Ollama backend.\nUse /backend to inspect your current provider.\n\n"))
			return m, nil
		}
		if len(parts) < 2 {
			m.append(dimStyle.Render("Usage: /pull <model-name>\nExample: /pull qwen2.5-coder:14b\n\nPopular models:\n  qwen2.5-coder:14b · qwen2.5-coder:7b · llama3.2 · codellama · mistral\n\nFind more at: https://ollama.com/library\n\n"))
		} else {
			modelName := parts[1]
			m.waiting = true
			m.isPulling = true
			m.pullModelName = modelName
			m.pullStatus = "starting..."
			b := m.backend
			return m, tea.Batch(m.spinner.Tick, func() tea.Msg {
				ch := make(chan backend.PullProgress, 10)
				errCh := make(chan error, 1)
				go func() {
					errCh <- b.PullModel(context.Background(), modelName, ch)
					close(ch)
				}()
				// Stream first progress message back
				for p := range ch {
					pp := p
					return pullMsg{progress: &pp}
				}
				if err := <-errCh; err != nil {
					return pullMsg{err: err}
				}
				return pullMsg{progress: nil}
			})
		}

	case "/save":
		if m.store == nil || m.conversationID == "" {
			m.append(dimStyle.Render("Nothing to save yet.\n\n"))
		} else {
			m.append(dimStyle.Render(fmt.Sprintf("Conversation saved (id: %s)\n\n", m.conversationID[:8])))
		}

	case "/history":
		if m.store == nil {
			m.append(errorStyle.Render("Storage not available.\n\n"))
		} else {
			convs, err := m.store.ListConversations()
			if err != nil {
				m.append(errorStyle.Render("Error: "+err.Error()) + "\n\n")
			} else if len(convs) == 0 {
				m.append(dimStyle.Render("No saved conversations yet.\n\n"))
			} else {
				m.historyMode = true
				m.historyList = newHistoryList(convs, m.width, m.height)
			}
		}

	case "/resume":
		if len(parts) < 2 {
			m.append(errorStyle.Render("Usage: /resume <id>\n\n"))
		} else if m.store == nil {
			m.append(errorStyle.Render("Storage not available.\n\n"))
		} else {
			return m.loadConversation(parts[1])
		}

	case "/clear":
		m.history = nil
		m.conversationID = ""
		m.content = welcomeContent(m.cfg, m.backend) + "\n"
		m.render()

	case "/quit", "/exit":
		return m, tea.Quit

	case "/compact":
		if len(m.history) == 0 {
			m.append(dimStyle.Render("Nothing to compact yet.\n\n"))
			m.textarea.Reset()
			return m, nil
		}
		m.append(dimStyle.Render("🗜  Compacting conversation... asking Billy to summarize...\n\n"))
		m.waiting = true
		m.textarea.Reset()
		return m, tea.Batch(m.spinner.Tick, m.compactHistory())

	case "/session":
		subArgs := parts[1:]
		if len(subArgs) == 0 || subArgs[0] == "save" {
			if m.store == nil {
				m.append(errorStyle.Render("Storage not available.\n\n"))
				m.textarea.Reset()
				return m, nil
			}
			name := fmt.Sprintf("checkpoint-%s", time.Now().Format("2006-01-02-15:04"))
			if len(subArgs) > 1 {
				name = strings.Join(subArgs[1:], "-")
			}
			m.append(dimStyle.Render("💾 Saving checkpoint... generating summary...\n\n"))
			m.waiting = true
			m.textarea.Reset()
			return m, tea.Batch(m.spinner.Tick, m.saveCheckpoint(name))
		}
		switch subArgs[0] {
		case "list":
			if m.store == nil {
				m.append(errorStyle.Render("Storage not available.\n\n"))
				break
			}
			checkpoints, err := m.store.AllCheckpoints()
			if err != nil || len(checkpoints) == 0 {
				m.append(dimStyle.Render("No checkpoints saved yet. Use /session to create one.\n\n"))
				break
			}
			var sb strings.Builder
			sb.WriteString("Session checkpoints:\n\n")
			for _, cp := range checkpoints {
				sb.WriteString(fmt.Sprintf("  %-30s  %s  (%d msgs)\n", cp.Name, cp.CreatedAt.Format("Jan 2 15:04"), cp.MessageCount))
			}
			sb.WriteString("\nUse: /session load <name>\n\n")
			m.append(dimStyle.Render(sb.String()))
		case "load":
			if len(subArgs) < 2 {
				m.append(errorStyle.Render("Usage: /session load <name>\n\n"))
				break
			}
			if m.store == nil {
				m.append(errorStyle.Render("Storage not available.\n\n"))
				break
			}
			cpName := strings.Join(subArgs[1:], "-")
			cp, err := m.store.GetCheckpointByName(cpName)
			if err != nil || cp == nil {
				m.append(errorStyle.Render(fmt.Sprintf("Checkpoint '%s' not found.\n\n", cpName)))
				break
			}
			m.history = []backend.Message{
				{Role: "system", Content: "Session checkpoint summary: " + cp.Summary},
			}
			m.compacted = true
			m.tokenEstimate = estimateTokens(m.history)
			m.append(dimStyle.Render(fmt.Sprintf("✅ Loaded checkpoint '%s'\n\nSummary: %s\n\n── Continuing from checkpoint ──\n\n", cp.Name, cp.Summary)))
		default:
			m.append(errorStyle.Render(fmt.Sprintf("Unknown /session subcommand: %s\nUsage: /session [save|list|load <name>]\n\n", subArgs[0])))
		}
		m.textarea.Reset()
		return m, nil

	case "/pwd":
		return m.cmdPwd()

	case "/cd":
		target := ""
		if len(parts) > 1 {
			target = strings.Join(parts[1:], " ")
		}
		return m.cmdCd(target)

	case "/ls":
		target := ""
		if len(parts) > 1 {
			target = strings.Join(parts[1:], " ")
		}
		return m.cmdLs(target)

	case "/git":
		return m.cmdGit()

	case "/suggest":
		if len(parts) < 2 {
			m.append(errorStyle.Render("Usage: /suggest <describe what you want to do>\nExample: /suggest list all Go files modified today\n\n"))
			m.textarea.Reset()
			return m, nil
		}
		task := strings.Join(parts[1:], " ")
		m.append(userStyle.Render("You >") + fmt.Sprintf(" /suggest %s\n\n", task))
		m.waiting = true
		m.textarea.Reset()
		return m, tea.Batch(m.spinner.Tick, m.suggestCmd(task))

	case "/explain":
		if len(parts) < 2 {
			m.append(errorStyle.Render("Usage: /explain <shell command>\nExample: /explain find . -name '*.go' -mtime -1\n\n"))
			m.textarea.Reset()
			return m, nil
		}
		shellCmd := strings.Join(parts[1:], " ")
		m.append(userStyle.Render("You >") + fmt.Sprintf(" /explain %s\n\n", shellCmd))
		m.waiting = true
		m.textarea.Reset()
		return m, tea.Batch(m.spinner.Tick, m.explainCmd(shellCmd))

	default:
		m.append(errorStyle.Render(fmt.Sprintf("Unknown command: %s  (try /help)\n\n", cmd)))
	}

	return m, nil
}

// --- Directory & shell helper commands ---

func (m ChatModel) cmdPwd() (ChatModel, tea.Cmd) {
	m.append(dimStyle.Render(fmt.Sprintf("📁 %s\n\n", m.workDir)))
	m.textarea.Reset()
	return m, nil
}

func (m ChatModel) cmdCd(target string) (ChatModel, tea.Cmd) {
	if target == "" || target == "~" {
		home, _ := os.UserHomeDir()
		target = home
	} else if strings.HasPrefix(target, "~/") {
		home, _ := os.UserHomeDir()
		target = filepath.Join(home, target[2:])
	} else if !filepath.IsAbs(target) {
		target = filepath.Join(m.workDir, target)
	}
	target = filepath.Clean(target)
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		m.append(errorStyle.Render(fmt.Sprintf("cd: no such directory: %s\n\n", target)))
		m.textarea.Reset()
		return m, nil
	}
	if err := os.Chdir(target); err != nil {
		m.append(errorStyle.Render(fmt.Sprintf("cd: %s\n\n", err)))
		m.textarea.Reset()
		return m, nil
	}
	m.workDir = target
	m.append(dimStyle.Render(fmt.Sprintf("📁 → %s\n\n", abbreviatePath(target))))
	m.textarea.Reset()
	return m, nil
}

func (m ChatModel) cmdLs(target string) (ChatModel, tea.Cmd) {
	dir := m.workDir
	if target != "" {
		if !filepath.IsAbs(target) {
			target = filepath.Join(m.workDir, target)
		}
		dir = filepath.Clean(target)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		m.append(errorStyle.Render(fmt.Sprintf("ls: %s\n\n", err)))
		m.textarea.Reset()
		return m, nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📂 %s\n\n", abbreviatePath(dir)))
	dirs, files := 0, 0
	for _, e := range entries {
		if e.IsDir() {
			sb.WriteString(fmt.Sprintf("  📁 %s/\n", e.Name()))
			dirs++
		} else {
			sb.WriteString(fmt.Sprintf("  📄 %s\n", e.Name()))
			files++
		}
	}
	sb.WriteString(fmt.Sprintf("\n  %d dirs, %d files\n\n", dirs, files))
	m.append(dimStyle.Render(sb.String()))
	m.textarea.Reset()
	return m, nil
}

func (m ChatModel) cmdGit() (ChatModel, tea.Cmd) {
	runGit := func(args ...string) string {
		c := exec.Command("git", args...)
		c.Dir = m.workDir
		out, err := c.CombinedOutput()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}

	branch := runGit("rev-parse", "--abbrev-ref", "HEAD")
	if branch == "" {
		m.append(dimStyle.Render("  Not a git repository.\n\n"))
		m.textarea.Reset()
		return m, nil
	}
	status := runGit("status", "--short")
	log := runGit("log", "--oneline", "-7")

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🌿 Branch: %s\n\n", branch))
	if status != "" {
		sb.WriteString("Changes:\n")
		for _, line := range strings.Split(status, "\n") {
			sb.WriteString("  " + line + "\n")
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("  Working tree clean\n\n")
	}
	if log != "" {
		sb.WriteString("Recent commits:\n")
		for _, line := range strings.Split(log, "\n") {
			sb.WriteString("  " + line + "\n")
		}
		sb.WriteString("\n")
	}
	m.append(dimStyle.Render(sb.String()))
	m.textarea.Reset()
	return m, nil
}

// compactHistory asks the model to summarize the conversation, then replaces
// history with [summary-system-msg] + last 6 messages.
func (m ChatModel) compactHistory() tea.Cmd {
	msgs := make([]backend.Message, len(m.history))
	copy(msgs, m.history)
	b := m.backend
	convID := m.conversationID
	s := m.store
	return func() tea.Msg {
		var sb strings.Builder
		sb.WriteString("Summarize the following conversation concisely. Focus on: decisions made, code written, problems solved, context the user wants remembered. Output only the summary, no preamble.\n\n")
		for _, msg := range msgs {
			sb.WriteString(fmt.Sprintf("[%s]: %s\n\n", msg.Role, msg.Content))
		}
		ctx, cancel := context.WithTimeout(context.Background(), backendRequestTimeout)
		defer cancel()
		summary, err := b.Chat(ctx, []backend.Message{
			{Role: "user", Content: sb.String()},
		}, backend.ChatOptions{Temperature: 0.3, NumPredict: 512})
		if err != nil {
			return chatMsg{err: fmt.Errorf("compact failed: %w", err)}
		}
		if s != nil && convID != "" {
			_ = s.UpdateCompactedSummary(convID, summary)
		}
		return compactMsg{summary: summary}
	}
}

// saveCheckpoint asks the model for a thorough summary and persists it as a named checkpoint.
func (m ChatModel) saveCheckpoint(name string) tea.Cmd {
	msgs := make([]backend.Message, len(m.history))
	copy(msgs, m.history)
	b := m.backend
	s := m.store
	convID := m.conversationID
	msgCount := len(msgs)
	return func() tea.Msg {
		var sb strings.Builder
		sb.WriteString("Summarize this conversation for a session checkpoint. Be thorough — this summary will be used to restore context later. Include: what was built, key decisions, current state, and what to do next.\n\n")
		for _, msg := range msgs {
			sb.WriteString(fmt.Sprintf("[%s]: %s\n\n", msg.Role, msg.Content))
		}
		ctx, cancel := context.WithTimeout(context.Background(), backendRequestTimeout)
		defer cancel()
		summary, err := b.Chat(ctx, []backend.Message{
			{Role: "user", Content: sb.String()},
		}, backend.ChatOptions{Temperature: 0.3, NumPredict: 1024})
		if err != nil {
			return checkpointMsg{err: fmt.Errorf("checkpoint failed: %w", err)}
		}
		if s != nil {
			_ = s.SaveCheckpoint(
				fmt.Sprintf("cp-%d", time.Now().UnixNano()),
				convID, name, summary, msgCount,
			)
		}
		return checkpointMsg{name: name, summary: summary}
	}
}

// suggestCmd asks the AI to suggest a shell command for a natural-language task.
func (m ChatModel) suggestCmd(task string) tea.Cmd {
	b := m.backend
	workDir := m.workDir
	return func() tea.Msg {
		prompt := fmt.Sprintf(
			"You are a shell command expert. The user is in directory: %s\n\n"+
				"Suggest the best shell command to: %s\n\n"+
				"Respond with:\n1. The exact command (in a code block)\n2. A brief explanation of what it does and any important flags.\n"+
				"If multiple approaches exist, show the best one first.",
			workDir, task,
		)
		ctx, cancel := context.WithTimeout(context.Background(), backendRequestTimeout)
		defer cancel()
		result, err := b.Chat(ctx, []backend.Message{
			{Role: "user", Content: prompt},
		}, backend.ChatOptions{Temperature: 0.2, NumPredict: 512})
		if err != nil {
			return suggestMsg{err: err}
		}
		return suggestMsg{content: result}
	}
}

// explainCmd asks the AI to explain what a shell command does.
func (m ChatModel) explainCmd(shellCmd string) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		prompt := fmt.Sprintf(
			"Explain the following shell command clearly and concisely. Break down each part, flag, and argument. "+
				"Mention any gotchas or common mistakes.\n\nCommand:\n```\n%s\n```",
			shellCmd,
		)
		ctx, cancel := context.WithTimeout(context.Background(), backendRequestTimeout)
		defer cancel()
		result, err := b.Chat(ctx, []backend.Message{
			{Role: "user", Content: prompt},
		}, backend.ChatOptions{Temperature: 0.2, NumPredict: 768})
		if err != nil {
			return explainMsg{err: err}
		}
		return explainMsg{content: result}
	}
}

// activateLicense validates the given key, encrypts it, and saves it to SQLite.
// promptShellRun shows a permission prompt before running a shell command.
func (m ChatModel) promptShellRun(shellCmd string) ChatModel {
	// Check session-level "always" permission
	prefix := strings.Fields(shellCmd)[0]
	if m.shellAlways[prefix] || m.shellAlways["*"] {
		m = m.executeShell(shellCmd)
		if len(m.cmdQueue) > 0 {
			return m.promptNextQueuedCmd()
		}
		return m
	}

	m.shellPending = shellCmd
	m.shellPickerIdx = 0
	m.textarea.Reset()
	m.textarea.Placeholder = "↑↓ select · Enter confirm · Esc cancel"
	return m
}

// executeShell runs a shell command and appends its output to the viewport.
func (m ChatModel) executeShell(shellCmd string) ChatModel {
	m.shellPending = ""
	m.textarea.Placeholder = defaultPromptPlaceholder
	m.textarea.Reset()

	cmdStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true)
	m.append(cmdStyle.Render("Command >") + " " + dimStyle.Render(shellCmd+"\n\n"))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	startDir := m.workDir
	output, finalDir, err := runShellFromDir(ctx, startDir, shellCmd)
	if finalDir != "" {
		m.workDir = finalDir
	}
	if output == "" {
		output = "(no output)"
	}
	var record string
	if err != nil {
		record = fmt.Sprintf("$ %s\n[exit error: %s]\n%s", shellCmd, err.Error(), output)
		m.appendCmdOutput(record, true)
	} else {
		record = fmt.Sprintf("$ %s\n%s", shellCmd, output)
		m.appendCmdOutput(record, false)
	}
	if m.agentMode {
		m.pendingCmdOutputs = append(m.pendingCmdOutputs, formatCommandFeedback(shellCmd, output, startDir, m.workDir, err))
	}
	return m
}

func runShellFromDir(ctx context.Context, workDir, shellCmd string) (output, finalDir string, err error) {
	wrapped := fmt.Sprintf("{ %s; }; status=$?; printf '\n%s%%s\n' \"$PWD\"; exit $status", shellCmd, pwdMarker)
	cmd := exec.CommandContext(ctx, "sh", "-c", wrapped) //nolint:gosec
	if workDir != "" {
		cmd.Dir = workDir
	}

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

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

// agentSystemPrompt is prepended when in AGENT mode.
const agentSystemPrompt = `You are Billy, an agentic AI coding assistant running locally via Ollama.

AGENT MODE is active. Your job is to take action, not just advise.

Rules:
- When the user asks you to run, create, install, build, or do ANYTHING that requires shell commands, provide the EXACT commands in ` + "```bash" + ` code blocks — never just describe them.
- When creating files, include the full file content in a code block. Put the filename as a comment on the first line (e.g. // main.go).
- Break complex tasks into sequential steps. Each step gets its own ` + "```bash" + ` block.
- After each command runs, the output and updated working directory are automatically fed back to you. Analyze them: if there are errors, diagnose and provide a corrected command. Keep iterating until the task succeeds.
- When the user's request has been satisfied and verification passes, stop. Do not output more bash blocks. Briefly explain that the task is complete.
- Do not keep re-running the same verification step after it has already succeeded.
- Be direct and action-oriented. Minimize prose, maximize commands.
- If something could be destructive (rm -rf, DROP TABLE, etc), warn clearly before the block.

Example good response:
  Here's how to initialize a Go module:
  ` + "```bash" + `
  mkdir myapp && cd myapp
  go mod init github.com/you/myapp
  ` + "```" + `
  Then create your main file:
  ` + "```bash" + `
  cat > main.go << 'EOF'
  package main
  import "fmt"
  func main() { fmt.Println("hello") }
  EOF
  ` + "```" + ``

// teachSystemPrompt is prepended when in TEACH mode.
const teachSystemPrompt = `You are Billy, an AI coding tutor running locally via Ollama.

TEACH MODE is active. Your job is to guide the user to learn by doing — never do the work for them.

Rules:
- Break tasks into small, numbered steps. Explain the concept behind each step before showing it.
- When a shell command is needed, provide it in a ` + "```bash" + ` code block — the UI will display it as something for the user to type themselves (not auto-run).
- Ask the user to try each step and report back before moving on.
- If the user gets stuck, give a targeted hint rather than the full solution.
- Encourage understanding: explain why, not just how.
- Be patient, supportive, and pedagogical.`

// adminConfig holds admin-controlled settings stored encrypted in the KV store.
type adminConfig struct {
	Locked     bool   `json:"locked"`
	LockMode   string `json:"lock_mode"`
	Curriculum string `json:"curriculum"`
}

// loadAdminConfig reads and decrypts the admin config from the store.
// Returns a zero-value adminConfig if none is set.
func (m ChatModel) loadAdminConfig() (adminConfig, error) {
	var ac adminConfig
	if m.store == nil {
		return ac, nil
	}
	cfgBytes, err := m.store.GetEncrypted("admin_config")
	if err != nil || len(cfgBytes) == 0 {
		return ac, err
	}
	err = json.Unmarshal(cfgBytes, &ac)
	return ac, err
}

// saveAdminConfig encrypts and persists the admin config.
func (m ChatModel) saveAdminConfig(ac adminConfig) error {
	if m.store == nil {
		return nil
	}
	b, err := json.Marshal(ac)
	if err != nil {
		return err
	}
	return m.store.SetEncrypted("admin_config", b)
}

// handleAdminCommand routes /admin sub-commands.
func (m ChatModel) handleAdminCommand(parts []string) (ChatModel, tea.Cmd) {
	m.textarea.Reset()
	subCmd := parts[0]
	switch subCmd {
	case "setup":
		if len(parts) < 2 {
			m.append(errorStyle.Render("Usage: /admin setup <pin>  (4–6 digits)\n\n"))
			return m, nil
		}
		pin := parts[1]
		if len(pin) < 4 || len(pin) > 6 {
			m.append(errorStyle.Render("PIN must be 4–6 digits.\n\n"))
			return m, nil
		}
		for _, ch := range pin {
			if ch < '0' || ch > '9' {
				m.append(errorStyle.Render("PIN must contain only digits.\n\n"))
				return m, nil
			}
		}
		if err := m.store.SetEncrypted("admin_pin", []byte(pin)); err != nil {
			m.append(errorStyle.Render("Failed to save PIN: " + err.Error() + "\n\n"))
			return m, nil
		}
		ac, _ := m.loadAdminConfig()
		ac.Locked = false
		if err := m.saveAdminConfig(ac); err != nil {
			m.append(errorStyle.Render("Failed to save admin config: " + err.Error() + "\n\n"))
			return m, nil
		}
		m.append(dimStyle.Render("✅ Admin PIN set. Use /admin lock to lock mode to TEACH.\n\n"))
		return m, nil

	case "lock":
		if !m.verifyAdminPIN() {
			m.append(errorStyle.Render("❌ No admin PIN set or PIN verification failed. Run /admin setup <pin> first.\n\n"))
			return m, nil
		}
		ac, _ := m.loadAdminConfig()
		ac.Locked = true
		ac.LockMode = "teach"
		if err := m.saveAdminConfig(ac); err != nil {
			m.append(errorStyle.Render("Failed to save admin config: " + err.Error() + "\n\n"))
			return m, nil
		}
		m.teachMode = true
		m.agentMode = false
		m.cmdQueue = nil
		m.append(dimStyle.Render("🔒 Mode locked to TEACH. Students cannot switch modes.\n\n"))
		return m, nil

	case "unlock":
		if !m.verifyAdminPIN() {
			m.append(errorStyle.Render("❌ No admin PIN set or PIN verification failed. Run /admin setup <pin> first.\n\n"))
			return m, nil
		}
		ac, _ := m.loadAdminConfig()
		ac.Locked = false
		if err := m.saveAdminConfig(ac); err != nil {
			m.append(errorStyle.Render("Failed to save admin config: " + err.Error() + "\n\n"))
			return m, nil
		}
		m.append(dimStyle.Render("🔓 Mode lock removed. Users can now switch modes freely.\n\n"))
		return m, nil

	case "curriculum":
		if len(parts) < 2 {
			m.append(errorStyle.Render("Usage: /admin curriculum <text>\n\n"))
			return m, nil
		}
		text := strings.Join(parts[1:], " ")
		ac, _ := m.loadAdminConfig()
		ac.Curriculum = text
		if err := m.saveAdminConfig(ac); err != nil {
			m.append(errorStyle.Render("Failed to save curriculum: " + err.Error() + "\n\n"))
			return m, nil
		}
		m.append(dimStyle.Render("✅ Curriculum context saved.\n\n"))
		return m, nil

	case "status":
		ac, err := m.loadAdminConfig()
		if err != nil {
			m.append(errorStyle.Render("Failed to load admin config: " + err.Error() + "\n\n"))
			return m, nil
		}
		pinSet := "not set"
		if m.store != nil {
			if pinBytes, err := m.store.GetEncrypted("admin_pin"); err == nil && len(pinBytes) > 0 {
				pinSet = "set"
			}
		}
		curriculum := ac.Curriculum
		if curriculum == "" {
			curriculum = "(none)"
		}
		m.append(dimStyle.Render(fmt.Sprintf("Admin status:\n  PIN:        %s\n  Locked:     %v\n  Lock mode:  %s\n  Curriculum: %s\n\n",
			pinSet, ac.Locked, ac.LockMode, curriculum)))
		return m, nil

	default:
		m.append(errorStyle.Render("Unknown admin command. Use /admin for help.\n\n"))
		return m, nil
	}
}

// verifyAdminPIN checks that a PIN has been set in the store.
// Returns true if a PIN is present (presence check only — interactive PIN entry
// is outside scope for TUI-embedded admin; the PIN itself guards the lock/unlock path).
func (m ChatModel) verifyAdminPIN() bool {
	if m.store == nil {
		return false
	}
	pinBytes, err := m.store.GetEncrypted("admin_pin")
	return err == nil && len(pinBytes) > 0
}

// sendHintCmd sends an extra hint-request message to the AI and returns the result as a chatMsg.
func (m ChatModel) sendHintCmd(hintMsg backend.Message) tea.Cmd {
	var memTexts []string
	if m.store != nil {
		if mems, err := m.store.ListMemories(); err == nil {
			for _, mem := range mems {
				memTexts = append(memTexts, mem.Content)
			}
		}
	}
	systemPrompt := memory.BuildSystemPrompt(memTexts)
	if m.teachMode {
		systemPrompt = teachSystemPrompt + "\n\n" + systemPrompt
	} else if m.agentMode {
		systemPrompt = agentSystemPrompt + "\n\n" + systemPrompt
	}

	fullHistory := make([]backend.Message, 0, len(m.history)+2)
	fullHistory = append(fullHistory, backend.Message{Role: "system", Content: systemPrompt})
	fullHistory = append(fullHistory, m.history...)
	fullHistory = append(fullHistory, hintMsg)

	opts := backend.ChatOptions{
		Temperature: m.cfg.Ollama.Temperature,
		NumPredict:  m.cfg.Ollama.NumPredict,
	}
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), backendRequestTimeout)
		defer cancel()
		content, err := b.Chat(ctx, fullHistory, opts)
		return chatMsg{content: content, err: err}
	}
}

// extractShellCommands finds all ```bash / ```sh / ```shell blocks in an AI
// response and returns each block's trimmed content as a command string.
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
		// Inside a block
		if strings.TrimSpace(line) == "```" {
			inBlock = false
			cmd := strings.TrimSpace(block.String())
			if cmd != "" {
				cmds = append(cmds, cmd)
			}
			continue
		}
		block.WriteString(line + "\n")
	}
	return cmds
}

func shouldStopAutopilot(records []string) bool {
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
		if strings.Contains(strings.ToLower(record), "[exit error:") || strings.Contains(strings.ToLower(record), "exit error:") {
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

func formatCommandFeedback(shellCmd, output, startDir, endDir string, runErr error) string {
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

// promptNextQueuedCmd pops the first command from cmdQueue and shows its
// permission prompt. Call this after a command completes or is skipped.
func (m ChatModel) promptNextQueuedCmd() ChatModel {
	if len(m.cmdQueue) == 0 {
		return m
	}
	cmd := m.cmdQueue[0]
	m.cmdQueue = m.cmdQueue[1:]
	return m.promptShellRun(cmd)
}

// flushCmdOutputs feeds all accumulated shell outputs back to the AI as a user
// message, triggering a new response so Billy can debug or continue the task.
func (m ChatModel) flushCmdOutputs() (ChatModel, tea.Cmd) {
	if len(m.pendingCmdOutputs) == 0 {
		return m, nil
	}
	combined := strings.Join(m.pendingCmdOutputs, "\n\n")
	if m.autopilotMode {
		m.lastAutoCommandSig = normalizeRecordBatch(m.pendingCmdOutputs)
		m.lastAutoCommandSuccess = !recordBatchHasError(m.pendingCmdOutputs)
	}
	if m.autopilotMode && shouldStopAutopilot(m.pendingCmdOutputs) {
		m.pendingCmdOutputs = nil
		m.append(dimStyle.Render("✅ Autopilot verified a successful result and stopped.\nBilly believes the task is complete.\n\n"))
		return m, nil
	}
	feedback := combined + fmt.Sprintf("\n\nCurrent working directory for the next step:\n```text\n%s\n```\n\nDecide whether the user's task is now complete. If it is complete, do not suggest any more commands. Briefly explain that it is done and stop. Only produce another bash block if more work is truly required.", m.workDir)
	m.pendingCmdOutputs = nil
	m.history = append(m.history, backend.Message{Role: "user", Content: feedback})
	m.tokenEstimate = estimateTokens(m.history)
	if m.store != nil && m.conversationID != "" {
		_ = m.store.AddMessage(uuid.New().String(), m.conversationID, "user", feedback)
	}
	cmdLabelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true)
	m.append(cmdLabelStyle.Render("Command >") + " " + dimStyle.Render("output sent to Billy...\n\n"))
	m.waiting = true
	return m, tea.Batch(m.sendChat(), m.spinner.Tick)
}
