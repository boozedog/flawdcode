package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type model struct {
	events []string
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if msg.String() == "q" || msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		m.events = append(m.events, fmt.Sprintf("key: %s", msg.String()))
	case tea.MouseWheelMsg:
		m.events = append(m.events, fmt.Sprintf("wheel: %s", msg.String()))
	case tea.MouseClickMsg:
		m.events = append(m.events, fmt.Sprintf("click: %s", msg.String()))
	case tea.MouseMsg:
		m.events = append(m.events, fmt.Sprintf("mouse: %s", msg.String()))
	}
	if len(m.events) > 20 {
		m.events = m.events[len(m.events)-20:]
	}
	return m, nil
}

func (m model) View() tea.View {
	s := "Mouse test - scroll trackpad or press keys (q to quit)\n\n"
	s += strings.Join(m.events, "\n")
	v := tea.NewView(s)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func main() {
	// Manually enable mouse mode before bubbletea starts
	fmt.Fprint(os.Stdout, "\x1b[?1002h\x1b[?1006h")

	if _, err := tea.NewProgram(model{}).Run(); err != nil {
		log.Fatal(err)
	}
}
