package tui

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
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

type pickerItem struct {
	cmd     string
	desc    string
	hasArgs bool // true = fill with trailing space; false = execute immediately on Enter
}

var commandList = []pickerItem{
	{"/activate", "Activate a Billy license key (prompts for key)", false},
	{"/clear", "Clear the current chat", false},
	{"/compact", "Summarize and compress context", false},
	{"/help", "Show all commands", false},
	{"/history", "Browse past conversations", false},
	{"/license", "Show current license / tier status", false},
	{"/memory", "List or manage memories", false},
	{"/mode", "Switch between agent and chat mode", true},
	{"/model", "List or switch Ollama models", true},
	{"/pull", "Download a model from Ollama", true},
	{"/quit", "Exit Billy", false},
	{"/resume", "Load a past conversation by ID", true},
	{"/run", "Run a shell command (with permission prompt)", true},
	{"/save", "Save current conversation", false},
	{"/session", "Save a session checkpoint", false},
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

// estimateTokens gives a rough token count for the history (4 chars ≈ 1 token).
func estimateTokens(history []backend.Message) int {
	total := 0
	for _, m := range history {
		total += len(m.Content) / 4
	}
	return total
}

// ChatModel is the Bubble Tea model for the main chat interface.
type ChatModel struct {
	cfg            *config.Config
	backend        backend.Backend
	store          *store.Store
	lic            *license.License // nil = free tier
	msgCount       int              // messages sent this session (for free limit)
	conversationID string
	history        []backend.Message
	content        string // raw accumulated content for the viewport
	viewport       viewport.Model
	textarea       textarea.Model
	spinner        spinner.Model
	historyMode    bool
	historyList    list.Model
	width          int
	height         int
	waiting        bool
	showPicker     bool
	pickerItems    []pickerItem
	pickerIdx      int
	activating     bool            // true while /activate key-entry prompt is shown
	shellPending   string          // shell command awaiting user permission
	shellAlways    map[string]bool // session-level "always run" prefixes
	cmdQueue       []string        // AI-suggested commands pending permission
	agentMode      bool            // true = agentic (default), false = chat only
	tokenEstimate  int             // rough token count for current history
	compacted      bool            // true if history has been compacted
}

// New creates a new ChatModel.
func New(cfg *config.Config, b backend.Backend, s *store.Store) ChatModel {
	ta := textarea.New()
	ta.Placeholder = "Ask Billy anything... (Enter to send, Ctrl+D to quit)"
	ta.Focus()
	ta.SetWidth(80)
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	ta.CharLimit = 4096

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))

	welcome := dimStyle.Render(fmt.Sprintf(
		"  Billy.sh 🐐  —  Model: %s\n  Type your message and press Enter. Use /help to see commands.\n\n",
		b.CurrentModel(),
	))

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
	}

	// Load license — SQLite encrypted store takes priority, config.toml is fallback
	if s != nil {
		if keyBytes, err := s.GetEncrypted("license_key"); err == nil && len(keyBytes) > 0 {
			if lic, err := license.Parse(string(keyBytes)); err == nil {
				m.lic = lic
			}
		}
	}
	if m.lic == nil && cfg.LicenseKey != "" {
		if lic, err := license.Parse(cfg.LicenseKey); err == nil {
			m.lic = lic
		}
	}

	return m
}

func (m ChatModel) Init() tea.Cmd {
	return textarea.Blink
}

func (m ChatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		taCmd  tea.Cmd
		vpCmd  tea.Cmd
		spCmd  tea.Cmd
	)

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width - 4
		m.viewport.Height = msg.Height - 8
		m.textarea.SetWidth(msg.Width - 4)
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
			m.textarea.Placeholder = "Ask Billy anything... (Enter to send, Ctrl+D to quit)"
			m.textarea.Reset()
			m.append(dimStyle.Render("Activation cancelled.\n\n"))
			return m, nil
		case tea.KeyEnter:
			key := strings.TrimSpace(m.textarea.Value())
			m.activating = false
			m.textarea.Placeholder = "Ask Billy anything... (Enter to send, Ctrl+D to quit)"
			m.textarea.Reset()
			return m.activateLicense(key), nil
		}
		m.textarea, taCmd = m.textarea.Update(msg)
		return m, taCmd
	}

	// ── Shell permission response ────────────────────────────────────────
	if m.shellPending != "" && msg.Type == tea.KeyEnter {
		resp := strings.ToLower(strings.TrimSpace(m.textarea.Value()))
		pending := m.shellPending
		m.shellPending = ""
		switch resp {
		case "y", "yes", "": // bare Enter = yes
			m = m.executeShell(pending)
		case "a", "always":
			prefix := strings.Fields(pending)[0]
			m.shellAlways[prefix] = true
			m = m.executeShell(pending)
		default: // n, no, skip, s
			m.textarea.Placeholder = "Ask Billy anything... (Enter to send, Ctrl+D to quit)"
			m.textarea.Reset()
			m.append(dimStyle.Render("Skipped.\n\n"))
			m.cmdQueue = nil // cancel rest of queue on explicit no
		}
		// Process next queued command if any
		if len(m.cmdQueue) > 0 {
			m = m.promptNextQueuedCmd()
		} else {
			m.textarea.Placeholder = "Ask Billy anything... (Enter to send, Ctrl+D to quit)"
		}
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

			// Freemium limits
			if (m.lic == nil || m.lic.Free()) && m.msgCount >= 20 {
				m.append(errorStyle.Render("⛔ Free tier limit reached (20 messages/session).\n\n") +
					dimStyle.Render("Upgrade to Pro for unlimited conversations:\n  https://billy.sh/upgrade\n\nOr use /license <key> to activate an existing license.\n\n"))
				m.textarea.Reset()
				return m, nil
			}
			if (m.lic == nil || m.lic.Free()) && m.msgCount == 15 {
				m.append(dimStyle.Render("⚠️  Approaching free tier limit (15/20 messages). Upgrade to Pro for unlimited: https://billy.sh/upgrade\n\n"))
			}
			m.msgCount++

			// Natural language memory detection
			if fact, ok := memory.DetectAndExtract(input); ok {
				if m.store != nil {
					if err := m.store.SaveMemory(uuid.New().String(), fact); err == nil {
						m.append(assistantStyle.Render("Billy") + "\n" +
							dimStyle.Render(fmt.Sprintf("Got it! I'll remember: \"%s\"\n", fact)) + "\n")
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

			m.history = append(m.history, backend.Message{Role: "user", Content: input})
			m.tokenEstimate = estimateTokens(m.history)
			m.append(userStyle.Render("You") + "\n" + input + "\n\n")

			// Persist user message
			if m.store != nil && m.conversationID != "" {
				_ = m.store.AddMessage(uuid.New().String(), m.conversationID, "user", input)
			}

			m.waiting = true
			return m, tea.Batch(m.sendChat(), m.spinner.Tick)
		}

	case pullMsg:
		if msg.err != nil {
			m.waiting = false
			m.append(errorStyle.Render("Pull failed: "+msg.err.Error()) + "\n\n")
		} else if msg.progress == nil {
			// Pull complete
			m.waiting = false
			m.append(dimStyle.Render("✅ Model downloaded successfully!\n\n"))
		} else {
			// Progress update — show inline, keep spinner going
			pct := ""
			if msg.progress.Total > 0 {
				pct = fmt.Sprintf(" %.0f%%", float64(msg.progress.Completed)/float64(msg.progress.Total)*100)
			}
			// Replace last line with progress (re-append truncates content)
			m.append(dimStyle.Render(fmt.Sprintf("  %s%s\n", msg.progress.Status, pct)))
		}
		return m, nil

	case chatMsg:
		m.waiting = false
		if msg.err != nil {
			m.append(errorStyle.Render("Error: "+msg.err.Error()) + "\n\n")
		} else {
			m.history = append(m.history, backend.Message{Role: "assistant", Content: msg.content})
			m.tokenEstimate = estimateTokens(m.history)
			m.append(assistantStyle.Render("Billy") + "\n" + msg.content + "\n\n")

			// Persist assistant message
			if m.store != nil && m.conversationID != "" {
				_ = m.store.AddMessage(uuid.New().String(), m.conversationID, "assistant", msg.content)
			}

			// In agent mode, detect shell commands and queue them for permission
			if m.agentMode {
				cmds := extractShellCommands(msg.content)
				if len(cmds) > 0 {
					m.cmdQueue = append(m.cmdQueue, cmds...)
					m = m.promptNextQueuedCmd()
				}
			}
		}
		return m, nil

	case spinner.TickMsg:
		if m.waiting {
			m.spinner, spCmd = m.spinner.Update(msg)
			return m, spCmd
		}
		return m, nil

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
	}

	m.textarea, taCmd = m.textarea.Update(msg)
	m.viewport, vpCmd = m.viewport.Update(msg)

	// Update command picker visibility based on current input.
	// Only reset pickerIdx when the filtered list actually changes (i.e. the
	// user typed a new character). Blink ticks and other non-key messages must
	// NOT reset the selection — that was causing the picker to jump to the top.
	val := m.textarea.Value()
	if strings.HasPrefix(val, "/") && !strings.Contains(val, " ") {
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
	if m.waiting {
		status = m.spinner.View() + dimStyle.Render(" Billy is thinking...")
	} else {
		badge := licenseBadge(m.lic)
		modeBadge := lipgloss.NewStyle().Foreground(lipgloss.Color("#38bdf8")).Bold(true).Render("[AGENT]")
		if !m.agentMode {
			modeBadge = dimStyle.Render("[CHAT]")
		}
		status = dimStyle.Render(fmt.Sprintf(" %s · %s  — PgUp/PgDn to scroll", m.backend.Name(), m.backend.CurrentModel())) + " " + modeBadge + " " + badge
		if m.tokenEstimate > 3072 {
			status += " " + lipgloss.NewStyle().Foreground(lipgloss.Color("#f59e0b")).Render(fmt.Sprintf("[~%dk tokens]", m.tokenEstimate/1000))
		}
	}

	return lipgloss.JoinVertical(
		lipgloss.Left,
		borderStyle.Width(m.width-2).Render(m.viewport.View()),
		status,
		m.renderPicker(),
		borderStyle.Width(m.width-2).Render(m.textarea.View()),
	)
}

// licenseBadge returns a styled tier badge for the status bar.
func licenseBadge(lic *license.License) string {
	if lic == nil || lic.Free() {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("[FREE]")
	}
	switch lic.EffectiveTier() {
	case license.TierPro:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#38bdf8")).Bold(true).Render("[PRO]")
	case license.TierPremium:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#f59e0b")).Bold(true).Render("[PREMIUM]")
	case license.TierTeam:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#a855f7")).Bold(true).Render("[TEAM]")
	case license.TierEnterprise:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#22c55e")).Bold(true).Render("[ENTERPRISE]")
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("[FREE]")
	}
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

// append adds raw text to the content buffer then re-renders the viewport.
func (m *ChatModel) append(text string) {
	m.content += text
	m.render()
}

// render word-wraps m.content to the current viewport width and scrolls to bottom.
// Using muesli/reflow which is ANSI-escape-aware, so styled text wraps correctly.
func (m *ChatModel) render() {
	width := m.viewport.Width - 2
	if width <= 0 {
		width = 78
	}
	m.viewport.SetContent(wordwrap.String(m.content, width))
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
		"  Billy.sh 🐐  —  Resumed conversation  ·  Model: %s\n\n",
		m.backend.CurrentModel(),
	))

	for _, msg := range msgs {
		m.history = append(m.history, backend.Message{Role: msg.Role, Content: msg.Content})
		switch msg.Role {
		case "user":
			m.content += userStyle.Render("You") + "\n" + msg.Content + "\n\n"
		case "assistant":
			m.content += assistantStyle.Render("Billy") + "\n" + msg.Content + "\n\n"
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
		content, err := b.Chat(context.Background(), fullHistory, opts)
		return chatMsg{content: content, err: err}
	}
}

// handleCommand routes slash commands.
func (m ChatModel) handleCommand(input string) (ChatModel, tea.Cmd) {
	parts := strings.Fields(input)
	cmd := parts[0]

	switch cmd {
	case "/activate":
		// Enter activation mode — intercept next Enter in Update()
		m.activating = true
		m.textarea.Reset()
		m.textarea.Placeholder = "Paste your BILLY-xxx license key and press Enter (Esc to cancel)..."
		m.append(dimStyle.Render("🔑 Enter your license key below and press Enter:\n\n"))
		return m, nil

	case "/license":
		// Show tier status only (activation is via /activate)
		if m.lic == nil || m.lic.Free() {
			m.append(dimStyle.Render("🔓 License: FREE tier\n\nLimits:\n• 20 messages per session\n• Memory not persisted between sessions\n• History limited to 5 conversations\n\nUpgrade at https://billy.sh\nUse /activate to enter a license key.\n\n"))
		} else {
			expStr := "Lifetime"
			if !m.lic.Expiry.IsZero() {
				expStr = m.lic.Expiry.Format("Jan 2, 2006")
			}
			seatsStr := ""
			if m.lic.Seats > 0 {
				seatsStr = fmt.Sprintf("\nSeats: %d", m.lic.Seats)
			}
			m.append(dimStyle.Render(fmt.Sprintf("✅ License: %s tier\nEmail: %s\nExpiry: %s%s\n\nAll features unlocked. 🐐\n\n",
				strings.ToUpper(string(m.lic.EffectiveTier())), m.lic.Email, expStr, seatsStr)))
		}
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
			modeStr := "agent"
			if !m.agentMode {
				modeStr = "chat"
			}
			m.append(dimStyle.Render(fmt.Sprintf("Current mode: %s\n\n  /mode agent  — Billy detects and offers to run commands\n  /mode chat   — conversation only, no command execution\n\n", strings.ToUpper(modeStr))))
		} else {
			switch parts[1] {
			case "agent":
				m.agentMode = true
				m.append(dimStyle.Render("✅ Switched to AGENT mode.\nBilly will detect commands in responses and ask to run them.\n\n"))
			case "chat":
				m.agentMode = false
				m.cmdQueue = nil
				m.append(dimStyle.Render("✅ Switched to CHAT mode.\nBilly will answer questions only — no command execution.\n\n"))
			default:
				m.append(errorStyle.Render("Unknown mode. Use: /mode agent  or  /mode chat\n\n"))
			}
		}
		m.textarea.Reset()
		return m, nil

	case "/help":
		modeStr := "AGENT (default)"
		if !m.agentMode {
			modeStr = "CHAT"
		}
		m.append(dimStyle.Render(fmt.Sprintf(`
Commands:
  /help              Show this help
  /activate          Activate a Billy license key (interactive prompt)
  /license           Show current license / tier status
  /mode [agent|chat] Switch mode (current: %s)
  /model             List installed models (active model highlighted)
  /model <name>      Switch to a different model
  /pull <name>       Download a new model from Ollama library
  /models            Alias for /model
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

Agent mode:
  When Billy suggests a command, a permission prompt appears.
  Press Enter or y=yes  a=always this session  n/s=skip

Natural language memory:
  "Remember that I prefer Go over Python"
  "Note that my name is Jonathan"
  "Don't forget I'm building Billy.sh"

Keyboard:
  PgUp / PgDn        Scroll conversation
  Ctrl+D / Ctrl+C    Quit

Popular models to pull:
  qwen2.5-coder:7b · llama3 · codellama · phi3 · gemma · mistral
  Full list: https://ollama.com/library

`, modeStr)))

	case "/models":
		// Alias for /model with no args
		return m.handleCommand("/model")

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
				m.append(dimStyle.Render("No models found. Use /pull <name> to download one.\n\n"))
			} else {
				var sb strings.Builder
				sb.WriteString("\nInstalled models (use /model <name> to switch):\n")
				for i, mo := range models {
					active := "  "
					if mo.Name == m.backend.CurrentModel() {
						active = "▶ "
					}
					sb.WriteString(fmt.Sprintf("  %s%-32s %s\n", active, mo.Name, dimStyle.Render(mo.Size)))
					_ = i
				}
				sb.WriteString("\n  Use /pull <name> to download a new model.\n\n")
				m.append(dimStyle.Render(sb.String()))
			}
		} else {
			m.backend.SetModel(parts[1])
			m.conversationID = ""
			m.history = nil
			m.append(dimStyle.Render(fmt.Sprintf("Switched to model: %s\n\n", parts[1])))
		}

	case "/pull":
		if len(parts) < 2 {
			m.append(dimStyle.Render("Usage: /pull <model-name>\nExample: /pull mistral\n\nPopular models:\n  mistral · llama3 · codellama · phi3 · gemma · neural-chat\n\nFind more at: https://ollama.com/library\n\n"))
		} else {
			modelName := parts[1]
			m.append(dimStyle.Render(fmt.Sprintf("Pulling %s from Ollama library...\n", modelName)))
			m.waiting = true
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
		m.content = dimStyle.Render(fmt.Sprintf(
			"  Billy.sh 🐐  —  Model: %s\n  Type your message and press Enter. Use /help to see commands.\n\n",
			m.backend.CurrentModel(),
		))
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

	default:
		m.append(errorStyle.Render(fmt.Sprintf("Unknown command: %s  (try /help)\n\n", cmd)))
	}

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
		summary, err := b.Chat(context.Background(), []backend.Message{
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
		summary, err := b.Chat(context.Background(), []backend.Message{
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

// activateLicense validates the given key, encrypts it, and saves it to SQLite.
func (m ChatModel) activateLicense(key string) ChatModel {
	if key == "" {
		m.append(errorStyle.Render("❌ No key entered. Use /activate to try again.\n\n"))
		return m
	}
	parsed, err := license.Parse(key)
	if err != nil {
		m.append(errorStyle.Render("❌ Invalid license key: "+err.Error()+"\n\n"))
		return m
	}
	if !parsed.IsActive() {
		m.append(errorStyle.Render("❌ License key has expired.\n\n"))
		return m
	}

	// Persist encrypted in SQLite (preferred) and plain in config (fallback)
	if m.store != nil {
		if err := m.store.SetEncrypted("license_key", []byte(key)); err != nil {
			m.append(errorStyle.Render("⚠️  Could not save license to database: "+err.Error()+"\n\n"))
		}
	}
	cfg := config.MustLoad()
	cfg.LicenseKey = key
	_ = config.Save(cfg)

	m.lic = parsed
	seatsNote := ""
	if parsed.Seats > 0 {
		seatsNote = fmt.Sprintf(" (%d seats)", parsed.Seats)
	}
	m.append(dimStyle.Render(fmt.Sprintf(
		"✅ License activated! Welcome to %s tier%s, %s 🎉\n\nYour key is encrypted and stored securely.\n\n",
		strings.ToUpper(string(parsed.EffectiveTier())), seatsNote, parsed.Email,
	)))
	return m
}

// promptShellRun shows a permission prompt before running a shell command.
func (m ChatModel) promptShellRun(shellCmd string) ChatModel {
	// Check session-level "always" permission
	prefix := strings.Fields(shellCmd)[0]
	if m.shellAlways[prefix] || m.shellAlways["*"] {
		return m.executeShell(shellCmd)
	}

	m.shellPending = shellCmd
	permBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#f59e0b")).
		Padding(0, 1).
		Render(fmt.Sprintf(
			"⚠️  Run this command?\n\n  %s\n\n"+
				"  [Y] Yes, once   [A] Always this session   [N] No, cancel",
			lipgloss.NewStyle().Foreground(lipgloss.Color("#38bdf8")).Render(shellCmd),
		))
	m.append(permBox + "\n\n")
	m.textarea.Reset()
	m.textarea.Placeholder = "y / a / n ..."
	return m
}

// executeShell runs a shell command and appends its output to the viewport.
func (m ChatModel) executeShell(shellCmd string) ChatModel {
	m.shellPending = ""
	m.textarea.Placeholder = "Ask Billy anything... (Enter to send, Ctrl+D to quit)"
	m.textarea.Reset()

	m.append(dimStyle.Render(fmt.Sprintf("$ %s\n", shellCmd)))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", shellCmd) //nolint:gosec
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	output := out.String()
	if output == "" {
		output = "(no output)"
	}
	if err != nil {
		m.append(errorStyle.Render("Exit error: "+err.Error()+"\n") + output + "\n\n")
	} else {
		m.append(output + "\n\n")
	}
	return m
}



// agentSystemPrompt is prepended when in AGENT mode.
const agentSystemPrompt = `You are Billy, an agentic AI coding assistant running locally via Ollama.

AGENT MODE is active. Your job is to take action, not just advise.

Rules:
- When the user asks you to run, create, install, build, or do ANYTHING that requires shell commands, provide the EXACT commands in ` + "```bash" + ` code blocks — never just describe them.
- When creating files, include the full file content in a code block. Put the filename as a comment on the first line (e.g. // main.go).
- Break complex tasks into sequential steps. Each step gets its own ` + "```bash" + ` block.
- After the user approves and runs a command, they will paste the output back. Adjust your next step based on it.
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
