package main

import (
	"os/exec"

	tea "charm.land/bubbletea/v2"
)

// ClaudeResponseMsg carries the result of a one-shot claude invocation.
type ClaudeResponseMsg struct {
	Prompt   string
	Response *ClaudeResponse
	Err      error
}

// ClaudeStreamStartMsg is sent when StreamClaude starts, carries the channel.
type ClaudeStreamStartMsg struct {
	Prompt string
	Ch     <-chan StreamMsg
	// Cmd is the underlying exec.Cmd, available for cancellation.
	Cmd *exec.Cmd
}

// ClaudeStreamChunkMsg carries one event during streaming.
type ClaudeStreamChunkMsg struct {
	Event          StreamEvent
	TextDelta      string // extracted text from text_delta, empty otherwise
	ThinkingDelta  string // extracted text from thinking_delta, empty otherwise
	InputJSONDelta string // extracted partial JSON from input_json_delta, empty otherwise
}

// ClaudeStreamDoneMsg is sent when the streaming process finishes.
type ClaudeStreamDoneMsg struct {
	Prompt   string
	Response *ClaudeResponse
	Err      error
}

// StreamMsg is the internal channel type (not a tea.Msg).
type StreamMsg struct {
	Event    *StreamEvent
	Done     bool
	Response *ClaudeResponse
	Err      error
}

// InteractiveStartMsg is sent when an interactive session starts successfully.
type InteractiveStartMsg struct {
	Session *InteractiveSession
}

// interactiveStreamStartMsg is sent when a prompt has been sent to the interactive session.
type interactiveStreamStartMsg struct {
	ch <-chan string
}

// InteractiveChunkMsg carries a text chunk from the interactive PTY session.
type InteractiveChunkMsg struct {
	Text string
}

// InteractiveDoneMsg signals the interactive response is complete.
type InteractiveDoneMsg struct {
	Err error
}

// waitForInteractiveMsg returns a tea.Cmd that reads one text chunk from the
// interactive session channel and converts it to the appropriate tea.Msg.
func waitForInteractiveMsg(ch <-chan string) tea.Cmd {
	return func() tea.Msg {
		text, ok := <-ch
		if !ok {
			return InteractiveDoneMsg{}
		}
		return InteractiveChunkMsg{Text: text}
	}
}
