package tui

import (
	"context"
	"fmt"
	"strings"

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

// pullMsg carries progress or completion back into the update loop.
type pullMsg struct {
	progress *backend.PullProgress // nil = done
	err      error
}

type chatMsg struct {
	content string
	err     error
}

// ChatModel is the Bubble Tea model for the main chat interface.
type ChatModel struct {
	cfg            *config.Config
	backend        backend.Backend
	store          *store.Store
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

	return ChatModel{
		cfg:      cfg,
		backend:  b,
		store:    s,
		content:  welcome,
		viewport: vp,
		textarea: ta,
		spinner:  sp,
	}
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

			if strings.HasPrefix(input, "/") {
				return m.handleCommand(input)
			}

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
			m.append(assistantStyle.Render("Billy") + "\n" + msg.content + "\n\n")

			// Persist assistant message
			if m.store != nil && m.conversationID != "" {
				_ = m.store.AddMessage(uuid.New().String(), m.conversationID, "assistant", msg.content)
			}
		}
		return m, nil

	case spinner.TickMsg:
		if m.waiting {
			m.spinner, spCmd = m.spinner.Update(msg)
			return m, spCmd
		}
		return m, nil
	}

	m.textarea, taCmd = m.textarea.Update(msg)
	m.viewport, vpCmd = m.viewport.Update(msg)
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
		status = dimStyle.Render(fmt.Sprintf(" %s · %s  — PgUp/PgDn to scroll", m.backend.Name(), m.backend.CurrentModel()))
	}

	return lipgloss.JoinVertical(
		lipgloss.Left,
		borderStyle.Width(m.width-2).Render(m.viewport.View()),
		status,
		borderStyle.Width(m.width-2).Render(m.textarea.View()),
	)
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
	case "/help":
		m.append(dimStyle.Render(`
Commands:
  /help              Show this help
  /model             List installed models (active model highlighted)
  /model <name>      Switch to a different model
  /pull <name>       Download a new model from Ollama library
  /models            Alias for /model
  /memory            List everything Billy remembers about you
  /memory forget <id> Delete a specific memory
  /memory clear      Wipe all memories
  /save              Save this conversation
  /history           Browse past conversations (arrow keys + Enter to load)
  /resume <id>       Jump directly to a conversation by ID
  /clear             Clear conversation history
  /quit, /exit       Exit Billy

Natural language memory:
  "Remember that I prefer Go over Python"
  "Note that my name is Jonathan"
  "Don't forget I'm building Billy.sh"

Keyboard:
  PgUp / PgDn        Scroll conversation
  Ctrl+D / Ctrl+C    Quit

Popular models to pull:
  mistral · llama3 · codellama · phi3 · gemma · neural-chat
  Full list: https://ollama.com/library

`))

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

	default:
		m.append(errorStyle.Render(fmt.Sprintf("Unknown command: %s  (try /help)\n\n", cmd)))
	}

	return m, nil
}


