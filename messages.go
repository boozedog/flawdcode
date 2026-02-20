package main

// ClaudeResponseMsg carries the result of a one-shot claude invocation.
type ClaudeResponseMsg struct {
	Prompt   string
	Response *ClaudeResponse
	Err      error
}

// DiagnosticMsg carries an internal diagnostic event for the debug tab.
type DiagnosticMsg struct {
	Label   string
	Message string
}
