package main

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/glamour"
)

type chatEntry struct {
	role string // "user" or "assistant"
	text string
}

// ChatModel is the chat tab: viewport (history) + textarea (input) + glamour rendering.
type ChatModel struct {
	viewport  viewport.Model
	textarea  textarea.Model
	entries   []chatEntry
	streaming bool
	width     int
	height    int
	renderer  *glamour.TermRenderer
	sendFn    func(string) (string, error)
}

// NewChatModel creates a new chat tab model.
func NewChatModel(sendFn func(string) (string, error)) ChatModel {
	ta := textarea.New()
	ta.Placeholder = "Type a message..."
	ta.SetHeight(3)
	ta.CharLimit = 0

	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))

	r, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(76),
	)

	return ChatModel{
		viewport: vp,
		textarea: ta,
		renderer: r,
		sendFn:   sendFn,
	}
}

// Init returns the initial command (focus textarea).
func (m ChatModel) Init() tea.Cmd {
	return m.textarea.Focus()
}

// Update handles messages for the chat tab.
func (m ChatModel) Update(msg tea.Msg) (ChatModel, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if msg.String() == "enter" {
			text := strings.TrimSpace(m.textarea.Value())
			if text != "" {
				m.textarea.Reset()
				m.entries = append(m.entries, chatEntry{role: "user", text: text})
				m.refreshViewport()

				sendFn := m.sendFn
				cmds = append(cmds, func() tea.Msg {
					line, err := sendFn(text)
					if err != nil {
						return ClaudeExitMsg{Err: err}
					}
					return UserInputMsg{Text: text, Line: line}
				})
			}
			return m, tea.Batch(cmds...)
		}

	case ClaudeOutputMsg:
		m = m.handleClaudeOutput(msg)
		return m, nil
	}

	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	if cmd != nil {
		cmds = append(cmds, cmd)
	}
	m.viewport, cmd = m.viewport.Update(msg)
	if cmd != nil {
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m ChatModel) handleClaudeOutput(msg ClaudeOutputMsg) ChatModel {
	switch msg.Type {
	case "assistant":
		text := ExtractAssistantText(msg.Line)
		if text == "" {
			return m
		}
		if m.streaming {
			if len(m.entries) > 0 && m.entries[len(m.entries)-1].role == "assistant" {
				m.entries[len(m.entries)-1].text = text
			}
		} else {
			m.entries = append(m.entries, chatEntry{role: "assistant", text: text})
			m.streaming = true
		}
		m.refreshViewport()

	case "result":
		m.streaming = false
	}
	return m
}

func (m *ChatModel) refreshViewport() {
	var sb strings.Builder
	userStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	claudeStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))

	for i, e := range m.entries {
		if i > 0 {
			sb.WriteString("\n")
		}
		switch e.role {
		case "user":
			sb.WriteString(userStyle.Render("You: "))
			sb.WriteString(e.text)
			sb.WriteString("\n")
		case "assistant":
			sb.WriteString(claudeStyle.Render("Claude: "))
			if m.renderer != nil {
				rendered, err := m.renderer.Render(e.text)
				if err == nil {
					sb.WriteString(strings.TrimSpace(rendered))
				} else {
					sb.WriteString(e.text)
				}
			} else {
				sb.WriteString(e.text)
			}
			sb.WriteString("\n")
		}
	}

	m.viewport.SetContent(sb.String())
	m.viewport.GotoBottom()
}

// SetSize updates the chat tab dimensions.
func (m *ChatModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	textareaHeight := 3
	viewportHeight := h - textareaHeight - 1

	m.viewport.SetWidth(w)
	m.viewport.SetHeight(viewportHeight)
	m.textarea.SetWidth(w)
	m.textarea.SetHeight(textareaHeight)

	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(w-4),
	)
	if err == nil {
		m.renderer = r
	}
	m.refreshViewport()
}

// View renders the chat tab.
func (m ChatModel) View() string {
	divider := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8")).
		Render(strings.Repeat("─", m.width))

	return fmt.Sprintf("%s\n%s\n%s",
		m.viewport.View(),
		divider,
		m.textarea.View(),
	)
}
