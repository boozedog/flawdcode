package main

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Model is the root TUI model with tabs for chat and raw log.
type Model struct {
	activeTab int // 0=chat, 1=raw
	width     int
	height    int
	chat      ChatModel
	rawlog    RawLogModel
	claude    *ClaudeProcess
	err       error
	exited    bool
}

// NewModel creates the root model.
func NewModel() Model {
	cp := NewClaudeProcess()
	m := Model{
		claude: cp,
		rawlog: NewRawLogModel(),
	}
	m.chat = NewChatModel(cp.SendMessage)
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.chat.Init(),
		m.startClaude(),
	)
}

func (m Model) startClaude() tea.Cmd {
	cp := m.claude
	return func() tea.Msg {
		if err := cp.Start(); err != nil {
			return ClaudeExitMsg{Err: err}
		}
		go cp.ReadLoop()
		return <-cp.msgCh
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "tab":
			m.activeTab = (m.activeTab + 1) % 2
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
		case "ctrl+c", "ctrl+q":
			m.claude.Close()
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		contentHeight := m.height - 1
		m.chat.SetSize(m.width, contentHeight)
		m.rawlog.SetSize(m.width, contentHeight)
		return m, nil

	case ClaudeOutputMsg:
		var cmd tea.Cmd
		m.chat, cmd = m.chat.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.rawlog, cmd = m.rawlog.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		cmds = append(cmds, m.claude.WaitForOutput())
		return m, tea.Batch(cmds...)

	case UserInputMsg:
		var cmd tea.Cmd
		m.rawlog, cmd = m.rawlog.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case ClaudeExitMsg:
		m.exited = true
		if msg.Err != nil {
			m.err = msg.Err
		}
		return m, nil
	}

	// Delegate to active tab
	var cmd tea.Cmd
	if m.activeTab == 0 {
		m.chat, cmd = m.chat.Update(msg)
	} else {
		m.rawlog, cmd = m.rawlog.Update(msg)
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
	if m.err != nil && m.activeTab == 0 && len(m.chat.entries) == 0 {
		errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
		content = errStyle.Render("Error: "+m.err.Error()) + "\n\nPress Ctrl+Q to quit."
	} else if m.activeTab == 0 {
		content = m.chat.View()
	} else {
		content = m.rawlog.View()
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

	tabs := []string{"Chat", "Raw JSONL"}
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
