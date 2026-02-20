package main

import "encoding/json"

// StreamMsg is the envelope for all NDJSON messages from Claude.
type StreamMsg struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype,omitempty"`
}

// ClaudeOutputMsg wraps a parsed NDJSON line from Claude's stdout.
type ClaudeOutputMsg struct {
	StreamMsg
	Line string
}

// UserInputMsg signals that we sent a message to Claude's stdin.
type UserInputMsg struct {
	Text string
	Line string
}

// ClaudeExitMsg signals the subprocess exited.
type ClaudeExitMsg struct {
	Err error
}

// AssistantMessage parsed from type=assistant.
type AssistantMessage struct {
	Message struct {
		Content []ContentBlock `json:"content"`
	} `json:"message"`
}

// ContentBlock represents a content block in an assistant message.
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// ResultMessage parsed from type=result.
type ResultMessage struct {
	Subtype    string  `json:"subtype"`
	Result     string  `json:"result"`
	IsError    bool    `json:"is_error"`
	DurationMs int     `json:"duration_ms"`
	TotalCost  float64 `json:"total_cost_usd"`
}

// UserMessageInput is what we write to Claude's stdin.
type UserMessageInput struct {
	Type    string             `json:"type"`
	Content UserMessageContent `json:"content"`
}

// UserMessageContent is the content payload for a user message.
type UserMessageContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ExtractAssistantText pulls text from an assistant NDJSON line.
func ExtractAssistantText(line string) string {
	var msg AssistantMessage
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		return ""
	}
	var out string
	for _, block := range msg.Message.Content {
		if block.Type == "text" && block.Text != "" {
			if out != "" {
				out += "\n"
			}
			out += block.Text
		}
	}
	return out
}
