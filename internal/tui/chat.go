package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/google/uuid"

	"github.com/jonathanforrider/billy/internal/backend"
	"github.com/jonathanforrider/billy/internal/config"
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

// chatMsg carries a response from the backend back into the update loop.
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
		m.viewport.SetContent(m.content)
		m.viewport.GotoBottom()
		return m, nil

	case tea.KeyMsg:
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

// append adds text to the content buffer, updates the viewport, and scrolls to bottom.
func (m *ChatModel) append(text string) {
	m.content += text
	m.viewport.SetContent(m.content)
	m.viewport.GotoBottom()
}

// sendChat fires off a chat request and returns the result as a chatMsg.
func (m ChatModel) sendChat() tea.Cmd {
	history := make([]backend.Message, len(m.history))
	copy(history, m.history)
	opts := backend.ChatOptions{
		Temperature: m.cfg.Ollama.Temperature,
		NumPredict:  m.cfg.Ollama.NumPredict,
	}
	b := m.backend
	return func() tea.Msg {
		content, err := b.Chat(context.Background(), history, opts)
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
  /help            Show this help
  /models          List available models
  /model <name>    Switch to a different model
  /save            Save this conversation
  /clear           Clear conversation history
  /quit, /exit     Exit Billy

Keyboard:
  PgUp / PgDn      Scroll through conversation
  Ctrl+D / Ctrl+C  Quit

`))

	case "/models":
		models, err := m.backend.ListModels(context.Background())
		if err != nil {
			m.append(errorStyle.Render("Error listing models: "+err.Error()) + "\n\n")
		} else {
			var sb strings.Builder
			sb.WriteString("\nAvailable models:\n")
			for _, mo := range models {
				sb.WriteString(fmt.Sprintf("  • %-30s %s\n", mo.Name, dimStyle.Render(mo.Size)))
			}
			sb.WriteString("\n")
			m.append(dimStyle.Render(sb.String()))
		}

	case "/model":
		if len(parts) < 2 {
			m.append(errorStyle.Render("Usage: /model <name>\n\n"))
		} else {
			m.backend.SetModel(parts[1])
			m.conversationID = "" // new model = new conversation
			m.history = nil
			m.append(dimStyle.Render(fmt.Sprintf("Switched to model: %s\n\n", parts[1])))
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
				m.append(dimStyle.Render("No saved conversations.\n\n"))
			} else {
				var sb strings.Builder
				sb.WriteString("\nSaved conversations:\n")
				for _, c := range convs {
					sb.WriteString(fmt.Sprintf("  %s  %s  (%s)\n",
						c.ID[:8],
						c.Title,
						c.UpdatedAt.Format(time.DateTime),
					))
				}
				sb.WriteString("\n")
				m.append(dimStyle.Render(sb.String()))
			}
		}

	case "/clear":
		m.history = nil
		m.conversationID = ""
		m.content = dimStyle.Render(fmt.Sprintf(
			"  Billy.sh 🐐  —  Model: %s\n  Type your message and press Enter. Use /help to see commands.\n\n",
			m.backend.CurrentModel(),
		))
		m.viewport.SetContent(m.content)

	case "/quit", "/exit":
		return m, tea.Quit

	default:
		m.append(errorStyle.Render(fmt.Sprintf("Unknown command: %s  (try /help)\n\n", cmd)))
	}

	return m, nil
}


