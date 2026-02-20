package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// RawLogModel is the raw JSONL tab with color-coded JSON lines.
type RawLogModel struct {
	viewport viewport.Model
	lines    []string
	width    int
	height   int
}

// NewRawLogModel creates a new raw log tab model.
func NewRawLogModel() RawLogModel {
	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	return RawLogModel{
		viewport: vp,
	}
}

// Update handles messages for the raw log tab.
func (m RawLogModel) Update(msg tea.Msg) (RawLogModel, tea.Cmd) {
	switch msg := msg.(type) {
	case ClaudeOutputMsg:
		m.addLine("\u2190", msg.Type, msg.Line)
		return m, nil
	case UserInputMsg:
		m.addLine("\u2192", "user_message", msg.Line)
		return m, nil
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m *RawLogModel) addLine(direction, msgType, rawLine string) {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, []byte(rawLine), "", "  "); err != nil {
		pretty.WriteString(rawLine)
	}

	var style lipgloss.Style
	switch msgType {
	case "system":
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	case "assistant":
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	case "result":
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
	case "rate_limit_event":
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	case "user_message":
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("13"))
	default:
		style = lipgloss.NewStyle()
	}

	dirStyle := lipgloss.NewStyle().Bold(true)
	header := dirStyle.Render(fmt.Sprintf("%s [%s]", direction, msgType))
	body := style.Render(pretty.String())

	m.lines = append(m.lines, header+"\n"+body)
	m.viewport.SetContent(strings.Join(m.lines, "\n\n"))
	m.viewport.GotoBottom()
}

// SetSize updates the raw log tab dimensions.
func (m *RawLogModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.viewport.SetWidth(w)
	m.viewport.SetHeight(h)
}

// View renders the raw log tab.
func (m RawLogModel) View() string {
	return m.viewport.View()
}
