package main

import (
	"flag"
	"log"

	tea "charm.land/bubbletea/v2"
)

func main() {
	wireLog := flag.Bool("wire-log", false, "write raw wire log to /tmp/flawdcode-*.jsonl")
	flag.Parse()

	wireLogEnabled = *wireLog

	p := tea.NewProgram(NewModel())
	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
