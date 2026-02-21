package main

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
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
	vp.SoftWrap = true
	vp.KeyMap.Left = key.NewBinding(key.WithDisabled())
	vp.KeyMap.Right = key.NewBinding(key.WithDisabled())
	return WireModel{viewport: vp}
}

func (m WireModel) Update(msg tea.Msg) (WireModel, tea.Cmd) {
	stdoutStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	stderrStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	tsStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	cmdStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	stdinStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("13"))

	fmtTS := func(t time.Time) string {
		return tsStyle.Render(t.Format("15:04:05.000"))
	}

	switch msg := msg.(type) {
	case ClaudeStreamStartMsg:
		m.lines = append(m.lines, fmtTS(time.Now())+" "+cmdStyle.Render("$ claude (streaming)"))
		m.lines = append(m.lines, fmtTS(time.Now())+" "+stdinStyle.Render("prompt> ")+msg.Prompt)
		wasAtBottom := m.viewport.AtBottom()
		m.viewport.SetContent(strings.Join(m.lines, "\n"))
		if wasAtBottom {
			m.viewport.GotoBottom()
		}
		return m, nil

	case ClaudeStreamChunkMsg:
		m.lines = append(m.lines, fmtTS(msg.Event.ReceivedAt)+" "+stdoutStyle.Render("stdout< ")+msg.Event.Raw)
		wasAtBottom := m.viewport.AtBottom()
		m.viewport.SetContent(strings.Join(m.lines, "\n"))
		if wasAtBottom {
			m.viewport.GotoBottom()
		}
		return m, nil

	case ClaudeStreamDoneMsg:
		if msg.Err != nil {
			m.lines = append(m.lines, fmtTS(time.Now())+" "+errStyle.Render(fmt.Sprintf("error: %s", msg.Err)))
		} else if msg.Response != nil {
			if msg.Response.Stderr != "" {
				now := time.Now()
				for _, line := range strings.Split(strings.TrimSpace(msg.Response.Stderr), "\n") {
					m.lines = append(m.lines, fmtTS(now)+" "+stderrStyle.Render("stderr| ")+line)
				}
			}
		}
		m.lines = append(m.lines, dimStyle.Render("---"))
		wasAtBottom := m.viewport.AtBottom()
		m.viewport.SetContent(strings.Join(m.lines, "\n"))
		if wasAtBottom {
			m.viewport.GotoBottom()
		}
		return m, nil

	case ClaudeResponseMsg:
		if msg.Err != nil {
			m.lines = append(m.lines, fmtTS(time.Now())+" "+errStyle.Render(fmt.Sprintf("error: %s", msg.Err)))
		} else {
			r := msg.Response
			m.lines = append(m.lines, fmtTS(r.StartedAt)+" "+cmdStyle.Render("$ "+strings.Join(r.Command, " ")))
			m.lines = append(m.lines, fmtTS(r.StartedAt)+" "+stdinStyle.Render("prompt> ")+r.Prompt)
			for _, ev := range r.Events {
				m.lines = append(m.lines, fmtTS(ev.ReceivedAt)+" "+stdoutStyle.Render("stdout< ")+ev.Raw)
			}
			if r.Stderr != "" {
				now := time.Now()
				for _, line := range strings.Split(strings.TrimSpace(r.Stderr), "\n") {
					m.lines = append(m.lines, fmtTS(now)+" "+stderrStyle.Render("stderr| ")+line)
				}
			}
			m.lines = append(m.lines, dimStyle.Render("---"))
		}
		wasAtBottom := m.viewport.AtBottom()
		m.viewport.SetContent(strings.Join(m.lines, "\n"))
		if wasAtBottom {
			m.viewport.GotoBottom()
		}
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
