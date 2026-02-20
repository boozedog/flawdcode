package main

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/glamour"
)

type chatEntry struct {
	role string // "user", "assistant", or "error"
	text string
}

// ChatModel is the chat tab: viewport (history) + textarea (input) + glamour rendering.
type ChatModel struct {
	viewport viewport.Model
	textarea textarea.Model
	entries  []chatEntry
	thinking bool
	width    int
	height   int
	renderer *glamour.TermRenderer
}

// NewChatModel creates a new chat tab model.
func NewChatModel() ChatModel {
	ta := textarea.New()
	ta.Placeholder = "Type a message..."
	ta.SetHeight(3)
	ta.CharLimit = 0

	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	vp.KeyMap.Left = key.NewBinding(key.WithDisabled())
	vp.KeyMap.Right = key.NewBinding(key.WithDisabled())

	r, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(76),
	)

	return ChatModel{
		viewport: vp,
		textarea: ta,
		renderer: r,
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
		if msg.String() == "shift+enter" {
			m.textarea.InsertRune('\n')
			return m, nil
		}
		if msg.String() == "enter" {
			text := strings.TrimSpace(m.textarea.Value())
			if text != "" && !m.thinking {
				m.textarea.Reset()
				m.entries = append(m.entries, chatEntry{role: "user", text: text})
				m.thinking = true
				m.refreshViewport()

				prompt := m.buildPrompt()
				cmds = append(cmds, func() tea.Msg {
					resp, err := RunClaude(prompt)
					return ClaudeResponseMsg{Prompt: prompt, Response: resp, Err: err}
				})
			}
			return m, tea.Batch(cmds...)
		}

	case ClaudeResponseMsg:
		m.thinking = false
		if msg.Err != nil {
			m.entries = append(m.entries, chatEntry{role: "error", text: msg.Err.Error()})
		} else {
			m.entries = append(m.entries, chatEntry{role: "assistant", text: msg.Response.AssistantText()})
		}
		m.refreshViewport()
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

// buildPrompt constructs a markdown prompt from the full conversation history.
func (m *ChatModel) buildPrompt() string {
	var sb strings.Builder
	for _, e := range m.entries {
		switch e.role {
		case "user":
			sb.WriteString("## User\n\n")
			sb.WriteString(e.text)
			sb.WriteString("\n\n")
		case "assistant":
			sb.WriteString("## Assistant\n\n")
			sb.WriteString(e.text)
			sb.WriteString("\n\n")
		}
	}
	return sb.String()
}

func (m *ChatModel) refreshViewport() {
	var sb strings.Builder
	userStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	claudeStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
	errStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	thinkStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)

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
		case "error":
			sb.WriteString(errStyle.Render("Error: "))
			sb.WriteString(e.text)
			sb.WriteString("\n")
		}
	}

	if m.thinking {
		sb.WriteString("\n")
		sb.WriteString(thinkStyle.Render("Thinking..."))
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
