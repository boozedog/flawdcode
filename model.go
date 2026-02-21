package main

import (
	tea "charm.land/bubbletea/v2"
)

// Model is the root TUI model.
type Model struct {
	width  int
	height int
	chat   ChatModel
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
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.chat.SetSize(m.width, m.height)
		return m, m.chat.textarea.Focus()
	}

	var cmd tea.Cmd
	m.chat, cmd = m.chat.Update(msg)
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
