package main

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const numTabs = 2

// Model is the root TUI model with tabs for chat and wire.
type Model struct {
	activeTab int // 0=chat, 1=wire
	width     int
	height    int
	chat      ChatModel
	wire      WireModel
}

// NewModel creates the root model.
func NewModel() Model {
	return Model{
		chat: NewChatModel(),
		wire: NewWireModel(),
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
			m.chat.scrollMode = false
			if m.activeTab == 0 {
				cmds = append(cmds, m.chat.textarea.Focus())
			} else {
				m.chat.textarea.Blur()
			}
			return m, tea.Batch(cmds...)
		case "ctrl+1":
			m.activeTab = 0
			m.chat.scrollMode = false
			return m, m.chat.textarea.Focus()
		case "ctrl+2":
			m.activeTab = 1
			m.chat.scrollMode = false
			m.chat.textarea.Blur()
			return m, nil
		case "ctrl+c", "ctrl+q":
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		tabBarHeight := 2
		contentHeight := m.height - tabBarHeight
		m.chat.SetSize(m.width, contentHeight)
		m.wire.SetSize(m.width, contentHeight)
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
		m.wire, cmd = m.wire.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case ClaudeStreamStartMsg, ClaudeStreamChunkMsg, ClaudeStreamDoneMsg:
		var cmd tea.Cmd
		m.chat, cmd = m.chat.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.wire, cmd = m.wire.Update(msg)
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
		m.wire, cmd = m.wire.Update(msg)
	}
	if cmd != nil {
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m Model) View() tea.View {
	if m.width == 0 {
		v := tea.NewView("Starting...")
		v.AltScreen = true
		v.MouseMode = tea.MouseModeCellMotion
		return v
	}

	tabBar := m.renderTabBar()

	var content string
	switch m.activeTab {
	case 0:
		content = m.chat.View()
	case 1:
		content = m.wire.View()
	}

	v := tea.NewView(tabBar + "\n\n" + content)
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

	tabs := []string{"Chat", "Wire"}
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
