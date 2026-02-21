package main

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

// DiagnosticMsg carries an internal diagnostic event for the debug tab.
type DiagnosticMsg struct {
	Label   string
	Message string
}
