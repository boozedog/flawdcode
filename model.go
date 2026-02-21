package main

import (
	"os/exec"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
)

// gracefulKill sends SIGTERM and falls back to SIGKILL after a timeout.
// The process is expected to be reaped by the StreamClaude goroutine;
// this only ensures escalation if SIGTERM is not honoured.
func gracefulKill(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	go func() {
		time.Sleep(3 * time.Second)
		// Best-effort SIGKILL; harmless if process already exited.
		_ = cmd.Process.Kill()
	}()
}

// Model is the root TUI model.
type Model struct {
	width  int
	height int
	chat   *ChatModel
}

// NewModel creates the root model.
func NewModel() Model {
	return Model{
		chat: NewChatModel(),
	}
}

func (m Model) Init() tea.Cmd {
	return m.chat.Init()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "ctrl+q":
			if m.chat.streamCmd != nil {
				gracefulKill(m.chat.streamCmd)
			}
			if m.chat.iSession != nil {
				m.chat.iSession.Close()
			}
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.chat.SetSize(m.width, m.height)
		return m, m.chat.textarea.Focus()
	}

	cmd := m.chat.Update(msg)
	return m, cmd
}

func (m Model) View() tea.View {
	if m.width == 0 {
		v := tea.NewView("Starting...")
		v.AltScreen = true
		v.MouseMode = tea.MouseModeCellMotion
		return v
	}

	v := tea.NewView(m.chat.View())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}
