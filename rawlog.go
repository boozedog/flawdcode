package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
	case ClaudeResponseMsg:
		if msg.Err != nil {
			m.addLine("✗", "error", msg.Err.Error())
		} else {
			// Show the prompt
			m.addLine("→", "prompt", msg.Prompt)

			// Show each stdout NDJSON line we received
			for _, ev := range msg.Response.Events {
				m.addLine("←", ev.Type, ev.Raw)
			}
		}
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

	ts := time.Now().Format("15:04:05.000")
	tsStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	style := styleForType(msgType)
	dirStyle := lipgloss.NewStyle().Bold(true)
	header := tsStyle.Render(ts) + " " + dirStyle.Render(fmt.Sprintf("%s [%s]", direction, msgType))
	body := style.Render(pretty.String())

	m.lines = append(m.lines, header+"\n"+body)
	m.viewport.SetContent(strings.Join(m.lines, "\n\n"))
	m.viewport.GotoBottom()
}

func styleForType(t string) lipgloss.Style {
	switch t {
	case "system":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	case "assistant":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	case "result":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
	case "user_message":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("13"))
	case "error":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	default:
		return lipgloss.NewStyle()
	}
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
