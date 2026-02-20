package main

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// WireModel shows the exact command, stdin, stdout, and stderr for each invocation.
type WireModel struct {
	viewport viewport.Model
	lines    []string
	width    int
	height   int
}

func NewWireModel() WireModel {
	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	return WireModel{viewport: vp}
}

func (m WireModel) Update(msg tea.Msg) (WireModel, tea.Cmd) {
	switch msg := msg.(type) {
	case ClaudeResponseMsg:
		cmdStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
		stdinStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("13"))
		stdoutStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
		stderrStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
		errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
		dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
		tsStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

		fmtTS := func(t time.Time) string {
			return tsStyle.Render(t.Format("15:04:05.000"))
		}

		if msg.Err != nil {
			m.lines = append(m.lines, fmtTS(time.Now())+" "+errStyle.Render(fmt.Sprintf("error: %s", msg.Err)))
		} else {
			r := msg.Response

			// The command
			m.lines = append(m.lines, fmtTS(r.StartedAt)+" "+cmdStyle.Render("$ "+strings.Join(r.Command, " ")))

			// prompt (passed as argument)
			m.lines = append(m.lines, fmtTS(r.StartedAt)+" "+stdinStyle.Render("prompt> ")+r.Prompt)

			// stdout (each raw NDJSON line)
			for _, ev := range r.Events {
				m.lines = append(m.lines, fmtTS(ev.ReceivedAt)+" "+stdoutStyle.Render("stdout< ")+ev.Raw)
			}

			// stderr
			if r.Stderr != "" {
				now := time.Now()
				for _, line := range strings.Split(strings.TrimSpace(r.Stderr), "\n") {
					m.lines = append(m.lines, fmtTS(now)+" "+stderrStyle.Render("stderr| ")+line)
				}
			}

			m.lines = append(m.lines, dimStyle.Render("---"))
		}

		m.viewport.SetContent(strings.Join(m.lines, "\n"))
		m.viewport.GotoBottom()
		return m, nil
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m *WireModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.viewport.SetWidth(w)
	m.viewport.SetHeight(h)
}

func (m WireModel) View() string {
	return m.viewport.View()
}
