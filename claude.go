package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// StreamEvent is a single NDJSON line from claude's stdout.
type StreamEvent struct {
	Type       string `json:"type"`
	Subtype    string `json:"subtype,omitempty"`
	Raw        string
	ReceivedAt time.Time
}

// TokenUsage holds token counts from the result or per-turn usage.
type TokenUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

// ClaudeResult is the final "result" event from claude.
type ClaudeResult struct {
	Type           string     `json:"type"`
	Subtype        string     `json:"subtype"`
	Result         string     `json:"result"`
	IsError        bool       `json:"is_error"`
	DurationMs     int        `json:"duration_ms"`
	DurationAPIMs  int        `json:"duration_api_ms"`
	NumTurns       int        `json:"num_turns"`
	CostUSD        float64    `json:"total_cost_usd"`
	SessionID      string     `json:"session_id"`
	Usage          TokenUsage `json:"usage"`
}

// ClaudeResponse holds everything from a single claude invocation.
type ClaudeResponse struct {
	Command    []string
	Prompt     string
	Events     []StreamEvent
	Result     ClaudeResult
	Stderr     string
	Model      string    // extracted from assistant events
	StopReason string    // extracted from assistant events
	StartedAt  time.Time // when the command was started
}

// ContentBlock represents a single block in an assistant message (text, tool_use, or tool_result).
type ContentBlock struct {
	Type    string          `json:"type"`
	Text    string          `json:"text,omitempty"`
	ID      string          `json:"id,omitempty"`
	Name    string          `json:"name,omitempty"`
	Input   json.RawMessage `json:"input,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`
}

// ToolResult represents a tool result event.
type ToolResult struct {
	ToolUseID string `json:"tool_use_id"`
	Content   string
	IsError   bool `json:"is_error"`
}

// ChatBlock represents a renderable block in the chat: text, tool call, or tool result.
type ChatBlock struct {
	Kind       string // "text", "tool_use", "tool_result"
	Text       string
	ToolName   string
	ToolID     string
	ToolInput  string
	ToolOutput string
	IsError    bool
}

// ExtractBlocks parses all assistant and tool_result events into an ordered list of ChatBlocks.
func (r *ClaudeResponse) ExtractBlocks() []ChatBlock {
	var blocks []ChatBlock

	for _, ev := range r.Events {
		switch ev.Type {
		case "assistant":
			var msg struct {
				Message struct {
					Content []ContentBlock `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal([]byte(ev.Raw), &msg) == nil {
				for _, block := range msg.Message.Content {
					switch block.Type {
					case "text":
						if block.Text != "" {
							blocks = append(blocks, ChatBlock{Kind: "text", Text: block.Text})
						}
					case "tool_use":
						inputStr := "{}"
						if len(block.Input) > 0 {
							var pretty bytes.Buffer
							if json.Indent(&pretty, block.Input, "", "  ") == nil {
								inputStr = pretty.String()
							} else {
								inputStr = string(block.Input)
							}
						}
						blocks = append(blocks, ChatBlock{
							Kind:      "tool_use",
							ToolName:  block.Name,
							ToolID:    block.ID,
							ToolInput: inputStr,
						})
					}
				}
			}
		case "content_block_start":
			var cbs struct {
				ContentBlock ContentBlock `json:"content_block"`
			}
			if json.Unmarshal([]byte(ev.Raw), &cbs) == nil && cbs.ContentBlock.Type == "tool_use" {
				inputStr := "{}"
				if len(cbs.ContentBlock.Input) > 0 {
					var pretty bytes.Buffer
					if json.Indent(&pretty, cbs.ContentBlock.Input, "", "  ") == nil {
						inputStr = pretty.String()
					} else {
						inputStr = string(cbs.ContentBlock.Input)
					}
				}
				blocks = append(blocks, ChatBlock{
					Kind:      "tool_use",
					ToolName:  cbs.ContentBlock.Name,
					ToolID:    cbs.ContentBlock.ID,
					ToolInput: inputStr,
				})
			}
		case "user":
			var msg struct {
				Message struct {
					Content []struct {
						Type      string `json:"type"`
						ToolUseID string `json:"tool_use_id"`
						Content   any    `json:"content"`
						IsError   bool   `json:"is_error"`
					} `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal([]byte(ev.Raw), &msg) == nil {
				for _, block := range msg.Message.Content {
					if block.Type == "tool_result" {
						output := ""
						switch v := block.Content.(type) {
						case string:
							output = v
						case []any:
							for _, item := range v {
								if m, ok := item.(map[string]any); ok {
									if t, ok := m["text"].(string); ok {
										output += t
									}
								}
							}
						default:
							b, _ := json.MarshalIndent(block.Content, "", "  ")
							output = string(b)
						}
						blocks = append(blocks, ChatBlock{
							Kind:       "tool_result",
							ToolID:     block.ToolUseID,
							ToolOutput: output,
							IsError:    block.IsError,
						})
					}
				}
			}
		}
	}

	return blocks
}

// AssistantText extracts the text content from assistant events.
func (r *ClaudeResponse) AssistantText() string {
	// Prefer result.result if present
	if r.Result.Result != "" {
		return r.Result.Result
	}
	// Fall back to extracting from assistant events
	var last string
	for _, ev := range r.Events {
		if ev.Type == "assistant" {
			var msg struct {
				Message struct {
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal([]byte(ev.Raw), &msg) == nil {
				for _, block := range msg.Message.Content {
					if block.Type == "text" && block.Text != "" {
						last = block.Text
					}
				}
			}
		}
	}
	return last
}

// RunClaude spawns claude in print mode with the prompt as an argument.
// If sessionID is non-empty, uses --resume to continue an existing session.
// Output is stream-json (NDJSON).
func RunClaude(prompt, sessionID string) (*ClaudeResponse, error) {
	args := []string{"-p", "--output-format", "stream-json", "--verbose"}
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}
	args = append(args, prompt)
	cmd := exec.Command("claude", args...)

	// Filter out CLAUDECODE env var
	env := os.Environ()
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, "CLAUDECODE=") {
			filtered = append(filtered, e)
		}
	}
	cmd.Env = filtered

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	startedAt := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude: %w", err)
	}

	// Read all NDJSON events from stdout
	var events []StreamEvent
	var result ClaudeResult
	var model, stopReason string

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var ev StreamEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		ev.Raw = line
		ev.ReceivedAt = time.Now()
		events = append(events, ev)

		switch ev.Type {
		case "result":
			json.Unmarshal([]byte(line), &result)
		case "assistant":
			var msg struct {
				Message struct {
					Model      string `json:"model"`
					StopReason string `json:"stop_reason"`
				} `json:"message"`
			}
			if json.Unmarshal([]byte(line), &msg) == nil {
				if msg.Message.Model != "" {
					model = msg.Message.Model
				}
				if msg.Message.StopReason != "" {
					stopReason = msg.Message.StopReason
				}
			}
		}
	}

	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("claude: %w\nstderr: %s", err, stderr.String())
	}

	return &ClaudeResponse{
		Command:    cmd.Args,
		Prompt:     prompt,
		Events:     events,
		Result:     result,
		Stderr:     stderr.String(),
		Model:      model,
		StopReason: stopReason,
		StartedAt:  startedAt,
	}, nil
}
