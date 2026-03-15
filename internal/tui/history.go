package tui

import (
	"fmt"
	"io"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jonathanforrider/billy/internal/store"
)

// historyItem implements list.Item for a past conversation.
type historyItem struct {
	conv store.Conversation
}

func (h historyItem) Title() string {
	return h.conv.Title
}

func (h historyItem) Description() string {
	return fmt.Sprintf("%s  ·  %s", h.conv.Model, h.conv.UpdatedAt.Format(time.DateTime))
}

func (h historyItem) FilterValue() string {
	return h.conv.Title
}

// historyDelegate customises list item rendering.
type historyDelegate struct{}

func (d historyDelegate) Height() int                             { return 2 }
func (d historyDelegate) Spacing() int                           { return 1 }
func (d historyDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }
func (d historyDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	hi, ok := item.(historyItem)
	if !ok {
		return
	}

	selected := index == m.Index()

	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	cursor := "  "

	if selected {
		titleStyle = titleStyle.Foreground(lipgloss.Color("12")).Bold(true)
		descStyle = descStyle.Foreground(lipgloss.Color("6"))
		cursor = "▶ "
	}

	fmt.Fprintf(w, "%s%s\n", cursor, titleStyle.Render(hi.Title()))
	fmt.Fprintf(w, "  %s", descStyle.Render(hi.Description()))
}

// newHistoryList builds a Bubble Tea list model from stored conversations.
func newHistoryList(convs []store.Conversation, width, height int) list.Model {
	items := make([]list.Item, len(convs))
	for i, c := range convs {
		items[i] = historyItem{conv: c}
	}

	l := list.New(items, historyDelegate{}, width-4, height-6)
	l.Title = "📜 Past Conversations  (Enter to load · Esc to cancel)"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.Styles.Title = lipgloss.NewStyle().
		Foreground(lipgloss.Color("5")).
		Bold(true).
		Padding(0, 1)
	l.Styles.NoItems = lipgloss.NewStyle().
		Foreground(lipgloss.Color("8")).
		Padding(1, 2)

	return l
}
