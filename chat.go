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
	role       string         // "user", "assistant", or "error"
	text       string         // plain text for user/error; fallback text for assistant
	blocks     []ChatBlock    // parsed content blocks for assistant responses
	result     ClaudeResult   // result metadata
	model      string         // model used
	stopReason string         // stop reason
	hasResult  bool
}

// ChatModel is the chat tab: viewport (history) + textarea (input) + glamour rendering.
type ChatModel struct {
	viewport  viewport.Model
	textarea  textarea.Model
	entries   []chatEntry
	thinking  bool
	width     int
	height    int
	renderer  *glamour.TermRenderer
	sessionID string // persist session for --resume
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

				sid := m.sessionID
				cmds = append(cmds, func() tea.Msg {
					resp, err := RunClaude(text, sid)
					return ClaudeResponseMsg{Prompt: text, Response: resp, Err: err}
				})
			}
			return m, tea.Batch(cmds...)
		}

	case ClaudeResponseMsg:
		m.thinking = false
		if msg.Err != nil {
			m.entries = append(m.entries, chatEntry{role: "error", text: msg.Err.Error()})
		} else {
			// Store session ID for future --resume calls
			if msg.Response.Result.SessionID != "" {
				m.sessionID = msg.Response.Result.SessionID
			}
			blocks := msg.Response.ExtractBlocks()
			m.entries = append(m.entries, chatEntry{
				role:       "assistant",
				text:       msg.Response.AssistantText(),
				blocks:     blocks,
				result:     msg.Response.Result,
				model:      msg.Response.Model,
				stopReason: msg.Response.StopReason,
				hasResult:  true,
			})
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

func (m *ChatModel) refreshViewport() {
	var sb strings.Builder

	// Styles
	userLabelStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("15")).
		Background(lipgloss.Color("4")).
		Padding(0, 1)
	claudeLabelStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("15")).
		Background(lipgloss.Color("5")).
		Padding(0, 1)
	errLabelStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("15")).
		Background(lipgloss.Color("1")).
		Padding(0, 1)
	thinkStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("5")).
		Bold(true)
	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8"))
	toolNameStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("3"))
	toolBorderStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8"))
	toolInputStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("6"))
	toolOutputStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("7"))
	toolErrStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("9"))
	metaKeyStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8")).
		Bold(true)
	metaValStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8"))
	metaSepStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240"))

	contentWidth := m.width - 4
	if contentWidth < 20 {
		contentWidth = 20
	}

	separator := dimStyle.Render(strings.Repeat("─", m.width))

	for i, e := range m.entries {
		if i > 0 {
			sb.WriteString("\n" + separator + "\n\n")
		}

		switch e.role {
		case "user":
			sb.WriteString(userLabelStyle.Render("YOU"))
			sb.WriteString("\n\n")
			sb.WriteString(e.text)
			sb.WriteString("\n")

		case "assistant":
			sb.WriteString(claudeLabelStyle.Render("CLAUDE"))
			sb.WriteString("\n")

			if len(e.blocks) > 0 {
				m.renderBlocks(&sb, e.blocks, toolNameStyle, toolBorderStyle,
					toolInputStyle, toolOutputStyle, toolErrStyle, dimStyle, contentWidth)
			} else if e.text != "" {
				sb.WriteString("\n")
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

			// Detailed metadata block
			if e.hasResult {
				sb.WriteString("\n")
				sep := metaSepStyle.Render(" │ ")

				// Line 1: model, stop reason, turns
				var line1 []string
				if e.model != "" {
					line1 = append(line1, metaKeyStyle.Render("model ")+metaValStyle.Render(e.model))
				}
				if e.stopReason != "" {
					line1 = append(line1, metaKeyStyle.Render("stop ")+metaValStyle.Render(e.stopReason))
				}
				if e.result.NumTurns > 0 {
					line1 = append(line1, metaKeyStyle.Render("turns ")+metaValStyle.Render(fmt.Sprintf("%d", e.result.NumTurns)))
				}
				if len(line1) > 0 {
					sb.WriteString("  " + strings.Join(line1, sep) + "\n")
				}

				// Line 2: tokens
				u := e.result.Usage
				totalIn := u.InputTokens
				totalOut := u.OutputTokens
				var line2 []string
				line2 = append(line2, metaKeyStyle.Render("in ")+metaValStyle.Render(formatTokens(totalIn)))
				line2 = append(line2, metaKeyStyle.Render("out ")+metaValStyle.Render(formatTokens(totalOut)))
				if u.CacheReadInputTokens > 0 {
					line2 = append(line2, metaKeyStyle.Render("cache_read ")+metaValStyle.Render(formatTokens(u.CacheReadInputTokens)))
				}
				if u.CacheCreationInputTokens > 0 {
					line2 = append(line2, metaKeyStyle.Render("cache_write ")+metaValStyle.Render(formatTokens(u.CacheCreationInputTokens)))
				}
				sb.WriteString("  " + strings.Join(line2, sep) + "\n")

				// Line 3: cost, duration, session
				var line3 []string
				line3 = append(line3, metaKeyStyle.Render("cost ")+metaValStyle.Render(fmt.Sprintf("$%.4f", e.result.CostUSD)))
				line3 = append(line3, metaKeyStyle.Render("wall ")+metaValStyle.Render(fmt.Sprintf("%dms", e.result.DurationMs)))
				if e.result.DurationAPIMs > 0 {
					line3 = append(line3, metaKeyStyle.Render("api ")+metaValStyle.Render(fmt.Sprintf("%dms", e.result.DurationAPIMs)))
				}
				line3 = append(line3, metaKeyStyle.Render("session ")+metaValStyle.Render(truncate(e.result.SessionID, 16)))
				sb.WriteString("  " + strings.Join(line3, sep) + "\n")
			}

		case "error":
			sb.WriteString(errLabelStyle.Render("ERROR"))
			sb.WriteString("\n\n")
			sb.WriteString(e.text)
			sb.WriteString("\n")
		}
	}

	if m.thinking {
		if len(m.entries) > 0 {
			sb.WriteString("\n" + separator + "\n\n")
		}
		sb.WriteString(claudeLabelStyle.Render("CLAUDE"))
		sb.WriteString("\n\n")
		sb.WriteString(thinkStyle.Render("  ◆ Thinking..."))
		sb.WriteString("\n")
	}

	m.viewport.SetContent(sb.String())
	m.viewport.GotoBottom()
}

func (m *ChatModel) renderBlocks(sb *strings.Builder, blocks []ChatBlock,
	toolNameStyle, toolBorderStyle, toolInputStyle, toolOutputStyle, toolErrStyle, dimStyle lipgloss.Style,
	contentWidth int,
) {
	// Group tool_use and tool_result by ToolID for inline rendering
	resultMap := make(map[string]*ChatBlock)
	for i := range blocks {
		if blocks[i].Kind == "tool_result" {
			resultMap[blocks[i].ToolID] = &blocks[i]
		}
	}

	for _, block := range blocks {
		switch block.Kind {
		case "text":
			sb.WriteString("\n")
			if m.renderer != nil {
				rendered, err := m.renderer.Render(block.Text)
				if err == nil {
					sb.WriteString(strings.TrimSpace(rendered))
				} else {
					sb.WriteString(block.Text)
				}
			} else {
				sb.WriteString(block.Text)
			}
			sb.WriteString("\n")

		case "tool_use":
			sb.WriteString("\n")

			boxWidth := contentWidth - 2
			if boxWidth < 20 {
				boxWidth = 20
			}

			header := fmt.Sprintf("┌─ %s %s", toolNameStyle.Render("⚙ "+block.ToolName),
				toolBorderStyle.Render(strings.Repeat("─", max(0, boxWidth-len(block.ToolName)-6))))
			sb.WriteString(toolBorderStyle.Render("  ") + header + "\n")

			// Tool input
			inputLines := strings.Split(block.ToolInput, "\n")
			for _, line := range inputLines {
				if len(line) > boxWidth-4 {
					line = line[:boxWidth-7] + "..."
				}
				sb.WriteString(toolBorderStyle.Render("  │ ") + toolInputStyle.Render(line) + "\n")
			}

			// Tool result (if matched)
			if result, ok := resultMap[block.ToolID]; ok {
				sb.WriteString(toolBorderStyle.Render("  ├─ "))
				if result.IsError {
					sb.WriteString(toolErrStyle.Render("✗ Error") + "\n")
				} else {
					sb.WriteString(dimStyle.Render("✓ Result") + "\n")
				}

				output := result.ToolOutput
				outputLines := strings.Split(output, "\n")
				maxLines := 15
				truncated := false
				if len(outputLines) > maxLines {
					outputLines = outputLines[:maxLines]
					truncated = true
				}
				for _, line := range outputLines {
					if len(line) > boxWidth-4 {
						line = line[:boxWidth-7] + "..."
					}
					style := toolOutputStyle
					if result.IsError {
						style = toolErrStyle
					}
					sb.WriteString(toolBorderStyle.Render("  │ ") + style.Render(line) + "\n")
				}
				if truncated {
					sb.WriteString(toolBorderStyle.Render("  │ ") + dimStyle.Render(fmt.Sprintf("... (%d more lines)", len(strings.Split(output, "\n"))-maxLines)) + "\n")
				}
			}

			// Close box
			footer := fmt.Sprintf("└%s", strings.Repeat("─", max(0, boxWidth)))
			sb.WriteString(toolBorderStyle.Render("  "+footer) + "\n")

		case "tool_result":
			continue
		}
	}
}

func formatTokens(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
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
