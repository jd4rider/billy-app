package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jonathanforrider/billy/internal/backend"
	"github.com/jonathanforrider/billy/internal/config"
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
	cfg      *config.Config
	backend  backend.Backend
	history  []backend.Message
	viewport viewport.Model
	textarea textarea.Model
	width    int
	height   int
	waiting  bool
	err      error
}

// New creates a new ChatModel.
func New(cfg *config.Config, b backend.Backend) ChatModel {
	ta := textarea.New()
	ta.Placeholder = "Ask Billy anything... (Enter to send, Ctrl+D to quit)"
	ta.Focus()
	ta.SetWidth(80)
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	ta.CharLimit = 4096

	vp := viewport.New(80, 20)
	vp.SetContent(welcomeMessage(b.CurrentModel()))

	return ChatModel{
		cfg:      cfg,
		backend:  b,
		viewport: vp,
		textarea: ta,
	}
}

func welcomeMessage(model string) string {
	return dimStyle.Render(fmt.Sprintf(
		"  Billy.sh 🐐  —  Model: %s\n  Type your message and press Enter. Use /help to see commands.\n",
		model,
	))
}

func (m ChatModel) Init() tea.Cmd {
	return textarea.Blink
}

func (m ChatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		taCmd tea.Cmd
		vpCmd tea.Cmd
	)

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width - 4
		m.viewport.Height = msg.Height - 8
		m.textarea.SetWidth(msg.Width - 4)
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

			// Handle slash commands
			if strings.HasPrefix(input, "/") {
				return m.handleCommand(input)
			}

			// Regular chat message
			m.history = append(m.history, backend.Message{Role: "user", Content: input})
			m.appendToView(userStyle.Render("You") + "\n" + input + "\n\n")
			m.waiting = true
			return m, m.sendChat()
		}

	case chatMsg:
		m.waiting = false
		if msg.err != nil {
			m.appendToView(errorStyle.Render("Error: "+msg.err.Error()) + "\n\n")
		} else {
			m.history = append(m.history, backend.Message{Role: "assistant", Content: msg.content})
			m.appendToView(assistantStyle.Render("Billy") + "\n" + msg.content + "\n\n")
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

	status := dimStyle.Render(fmt.Sprintf(" %s · %s", m.backend.Name(), m.backend.CurrentModel()))
	if m.waiting {
		status = dimStyle.Render(" thinking...")
	}

	return lipgloss.JoinVertical(
		lipgloss.Left,
		borderStyle.Width(m.width-2).Render(m.viewport.View()),
		status,
		borderStyle.Width(m.width-2).Render(m.textarea.View()),
	)
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

// appendToView adds text to the viewport and scrolls to the bottom.
func (m *ChatModel) appendToView(text string) {
	current := m.viewport.View()
	_ = current
	// Re-render full content by appending to a content buffer
	m.viewport.SetContent(m.viewport.View() + text)
	m.viewport.GotoBottom()
}

// handleCommand routes slash commands.
func (m ChatModel) handleCommand(input string) (ChatModel, tea.Cmd) {
	parts := strings.Fields(input)
	cmd := parts[0]

	switch cmd {
	case "/help":
		help := `
Commands:
  /help          Show this help
  /models        List available models
  /model <name>  Switch to a different model
  /clear         Clear conversation history
  /quit, /exit   Exit Billy

`
		m.appendToView(dimStyle.Render(help))

	case "/models":
		models, err := m.backend.ListModels(context.Background())
		if err != nil {
			m.appendToView(errorStyle.Render("Error listing models: "+err.Error()) + "\n\n")
		} else {
			var sb strings.Builder
			sb.WriteString("\nAvailable models:\n")
			for _, mo := range models {
				sb.WriteString(fmt.Sprintf("  • %s  %s\n", mo.Name, dimStyle.Render(mo.Size)))
			}
			sb.WriteString("\n")
			m.appendToView(dimStyle.Render(sb.String()))
		}

	case "/model":
		if len(parts) < 2 {
			m.appendToView(errorStyle.Render("Usage: /model <name>\n\n"))
		} else {
			m.backend.SetModel(parts[1])
			m.appendToView(dimStyle.Render(fmt.Sprintf("Switched to model: %s\n\n", parts[1])))
		}

	case "/clear":
		m.history = nil
		m.viewport.SetContent(welcomeMessage(m.backend.CurrentModel()))

	case "/quit", "/exit":
		return m, tea.Quit

	default:
		m.appendToView(errorStyle.Render(fmt.Sprintf("Unknown command: %s  (try /help)\n\n", cmd)))
	}

	return m, nil
}
