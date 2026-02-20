package main

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// StderrModel is the debug tab showing stderr and diagnostics from each invocation.
type StderrModel struct {
	viewport viewport.Model
	lines    []string
	width    int
	height   int
}

// NewStderrModel creates a new debug tab model.
func NewStderrModel() StderrModel {
	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	return StderrModel{
		viewport: vp,
	}
}

// Update handles messages for the debug tab.
func (m StderrModel) Update(msg tea.Msg) (StderrModel, tea.Cmd) {
	switch msg := msg.(type) {
	case ClaudeResponseMsg:
		if msg.Err != nil {
			m.addEntry("error", "9", msg.Err.Error())
		} else {
			r := msg.Response.Result
			m.addEntry("result", "10", fmt.Sprintf(
				"subtype=%s duration=%dms cost=$%.4f",
				r.Subtype, r.DurationMs, r.CostUSD,
			))
			if msg.Response.Stderr != "" {
				for _, line := range strings.Split(strings.TrimSpace(msg.Response.Stderr), "\n") {
					m.addEntry("stderr", "11", line)
				}
			}
		}
		return m, nil

	case DiagnosticMsg:
		m.addEntry(msg.Label, "11", msg.Message)
		return m, nil
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m *StderrModel) addEntry(label, color, text string) {
	ts := time.Now().Format("15:04:05.000")
	tsStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	labelStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(color))
	line := fmt.Sprintf("%s %s %s", tsStyle.Render(ts), labelStyle.Render(fmt.Sprintf("[%-6s]", label)), text)
	m.lines = append(m.lines, line)
	m.viewport.SetContent(strings.Join(m.lines, "\n"))
	m.viewport.GotoBottom()
}

// SetSize updates the debug tab dimensions.
func (m *StderrModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.viewport.SetWidth(w)
	m.viewport.SetHeight(h)
}

// View renders the debug tab.
func (m StderrModel) View() string {
	return m.viewport.View()
}
