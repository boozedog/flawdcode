package main

import (
	"flag"
	"log"

	tea "charm.land/bubbletea/v2"
)

func main() {
	wireLog := flag.Bool("wire-log", false, "write raw wire log to /tmp/flawdcode-*.jsonl")
	interactive := flag.Bool("interactive", false, "use goexpect-based interactive session (experimental)")
	flag.Parse()

	SetWireLogEnabled(*wireLog)

	m := NewModel()
	m.chat.interactive = *interactive
	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
