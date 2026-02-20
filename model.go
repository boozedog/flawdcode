package main

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const numTabs = 4

// Model is the root TUI model with tabs for chat, raw log, wire, and debug.
type Model struct {
	activeTab int // 0=chat, 1=raw, 2=wire, 3=debug
	width     int
	height    int
	chat      ChatModel
	rawlog    RawLogModel
	wire      WireModel
	stderrlog StderrModel
}

// NewModel creates the root model.
func NewModel() Model {
	return Model{
		chat:      NewChatModel(),
		rawlog:    NewRawLogModel(),
		wire:      NewWireModel(),
		stderrlog: NewStderrModel(),
	}
}

func (m Model) Init() tea.Cmd {
	return m.chat.Init()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "tab":
			m.activeTab = (m.activeTab + 1) % numTabs
			if m.activeTab == 0 {
				cmds = append(cmds, m.chat.textarea.Focus())
			} else {
				m.chat.textarea.Blur()
			}
			return m, tea.Batch(cmds...)
		case "ctrl+1":
			m.activeTab = 0
			return m, m.chat.textarea.Focus()
		case "ctrl+2":
			m.activeTab = 1
			m.chat.textarea.Blur()
			return m, nil
		case "ctrl+3":
			m.activeTab = 2
			m.chat.textarea.Blur()
			return m, nil
		case "ctrl+4":
			m.activeTab = 3
			m.chat.textarea.Blur()
			return m, nil
		case "ctrl+c", "ctrl+q":
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		contentHeight := m.height - 1
		m.chat.SetSize(m.width, contentHeight)
		m.rawlog.SetSize(m.width, contentHeight)
		m.wire.SetSize(m.width, contentHeight)
		m.stderrlog.SetSize(m.width, contentHeight)
		if m.activeTab == 0 {
			return m, m.chat.textarea.Focus()
		}
		return m, nil

	case ClaudeResponseMsg:
		var cmd tea.Cmd
		m.chat, cmd = m.chat.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.rawlog, cmd = m.rawlog.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.wire, cmd = m.wire.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.stderrlog, cmd = m.stderrlog.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	}

	// Delegate to active tab
	var cmd tea.Cmd
	switch m.activeTab {
	case 0:
		m.chat, cmd = m.chat.Update(msg)
	case 1:
		m.rawlog, cmd = m.rawlog.Update(msg)
	case 2:
		m.wire, cmd = m.wire.Update(msg)
	case 3:
		m.stderrlog, cmd = m.stderrlog.Update(msg)
	}
	if cmd != nil {
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m Model) View() tea.View {
	if m.width == 0 {
		return tea.NewView("Starting...")
	}

	tabBar := m.renderTabBar()

	var content string
	switch m.activeTab {
	case 0:
		content = m.chat.View()
	case 1:
		content = m.rawlog.View()
	case 2:
		content = m.wire.View()
	case 3:
		content = m.stderrlog.View()
	}

	v := tea.NewView(tabBar + "\n" + content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m Model) renderTabBar() string {
	activeStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("15")).
		Background(lipgloss.Color("4")).
		Padding(0, 1)
	inactiveStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("7")).
		Padding(0, 1)
	helpStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8"))

	tabs := []string{"Chat", "Raw JSON", "Wire", "Debug"}
	var parts []string
	for i, tab := range tabs {
		if i == m.activeTab {
			parts = append(parts, activeStyle.Render(tab))
		} else {
			parts = append(parts, inactiveStyle.Render(tab))
		}
	}

	bar := lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	help := helpStyle.Render("  Tab: switch | Ctrl+Q: quit")
	return bar + help
}
