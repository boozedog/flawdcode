package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
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

// TaskResultMeta holds subagent metadata from the tool_use_result JSON field.
type TaskResultMeta struct {
	AgentID           string
	TotalDurationMs   int
	TotalTokens       int
	TotalToolUseCount int
}

// BlockKind identifies the type of a ChatBlock.
type BlockKind string

const (
	BlockText       BlockKind = "text"
	BlockToolUse    BlockKind = "tool_use"
	BlockToolResult BlockKind = "tool_result"
)

// ChatBlock represents a renderable block in the chat: text, tool call, or tool result.
// Use the constructor functions (NewTextBlock, NewToolUseBlock, NewToolResultBlock) for clarity.
type ChatBlock struct {
	Kind BlockKind

	// Text fields — set when Kind=BlockText
	Text string

	// Tool fields — set when Kind=BlockToolUse or BlockToolResult
	ToolName   string
	ToolID     string
	ToolInput  string
	ToolOutput string
	IsError    bool

	// Task (subagent) fields — only set when Kind=BlockToolUse and IsTask=true
	IsTask           bool
	TaskDescription  string
	TaskSubagentType string
	TaskPrompt       string
	TaskSubBlocks    []ChatBlock
	TaskMeta         *TaskResultMeta
}

// NewTextBlock creates a text content block.
func NewTextBlock(text string) ChatBlock {
	return ChatBlock{Kind: BlockText, Text: text}
}

// NewToolUseBlock creates a tool use block.
func NewToolUseBlock(name, id, input string) ChatBlock {
	return ChatBlock{Kind: BlockToolUse, ToolName: name, ToolID: id, ToolInput: input}
}

// NewTaskBlock creates a Task (subagent) tool use block.
func NewTaskBlock(id, input string) ChatBlock {
	cb := ChatBlock{Kind: BlockToolUse, ToolName: "Task", ToolID: id, ToolInput: input, IsTask: true}
	parseTaskInput(&cb, input)
	return cb
}

// NewToolResultBlock creates a tool result block.
func NewToolResultBlock(toolID, output string, isError bool) ChatBlock {
	return ChatBlock{Kind: BlockToolResult, ToolID: toolID, ToolOutput: output, IsError: isError}
}

// parseTaskInput extracts Task tool input fields from JSON into the ChatBlock.
func parseTaskInput(cb *ChatBlock, rawInput string) {
	var input struct {
		Description  string `json:"description"`
		SubagentType string `json:"subagent_type"`
		Prompt       string `json:"prompt"`
	}
	if json.Unmarshal([]byte(rawInput), &input) == nil {
		cb.TaskDescription = input.Description
		cb.TaskSubagentType = input.SubagentType
		cb.TaskPrompt = input.Prompt
	}
}

// extractParentToolUseID returns the parent_tool_use_id from a raw JSON event, or "".
func extractParentToolUseID(raw string) string {
	var ev struct {
		ParentToolUseID *string `json:"parent_tool_use_id"`
	}
	if json.Unmarshal([]byte(raw), &ev) == nil && ev.ParentToolUseID != nil && *ev.ParentToolUseID != "" {
		return *ev.ParentToolUseID
	}
	return ""
}

// findTaskBlockIndex returns the index of the Task block with the given ToolID, or -1.
func findTaskBlockIndex(blocks []ChatBlock, toolID string) int {
	for i := range blocks {
		if blocks[i].Kind == BlockToolUse && blocks[i].IsTask && blocks[i].ToolID == toolID {
			return i
		}
	}
	return -1
}

// parseToolUseResult extracts TaskResultMeta from the tool_use_result JSON field of a user event.
func parseToolUseResult(raw string) *TaskResultMeta {
	var ev struct {
		ToolUseResult *struct {
			AgentID           string `json:"agentId"`
			TotalDurationMs   int    `json:"totalDurationMs"`
			TotalTokens       int    `json:"totalTokens"`
			TotalToolUseCount int    `json:"totalToolUseCount"`
		} `json:"tool_use_result"`
	}
	if json.Unmarshal([]byte(raw), &ev) == nil && ev.ToolUseResult != nil {
		return &TaskResultMeta{
			AgentID:           ev.ToolUseResult.AgentID,
			TotalDurationMs:   ev.ToolUseResult.TotalDurationMs,
			TotalTokens:       ev.ToolUseResult.TotalTokens,
			TotalToolUseCount: ev.ToolUseResult.TotalToolUseCount,
		}
	}
	return nil
}

// extractToolResultContent converts tool result content (string, array, or other) to a string.
// If stripAgentID is true, text blocks prefixed with "agentId:" are excluded (used for Task results).
func extractToolResultContent(content any, stripAgentID bool) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var out string
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if t, ok := m["text"].(string); ok {
					if stripAgentID && strings.HasPrefix(t, "agentId:") {
						continue
					}
					out += t
				}
			}
		}
		return out
	default:
		b, _ := json.MarshalIndent(content, "", "  ")
		return string(b)
	}
}

// ExtractBlocks parses all assistant and tool_result events into an ordered list of ChatBlocks.
// Subagent events (parent_tool_use_id set) are grouped into their parent Task block's TaskSubBlocks.
func (r *ClaudeResponse) ExtractBlocks() []ChatBlock {
	var blocks []ChatBlock

	for _, ev := range r.Events {
		parentID := extractParentToolUseID(ev.Raw)

		switch ev.Type {
		case "assistant":
			var msg struct {
				Message struct {
					Content []ContentBlock `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal([]byte(ev.Raw), &msg) != nil {
				continue
			}

			if parentID != "" {
				// Subagent assistant event — route tool_use blocks to parent Task
				for _, block := range msg.Message.Content {
					if block.Type == "tool_use" {
						inputStr := "{}"
						if len(block.Input) > 0 {
							inputStr = prettyJSON(block.Input)
						}
						if idx := findTaskBlockIndex(blocks, parentID); idx >= 0 {
							blocks[idx].TaskSubBlocks = append(blocks[idx].TaskSubBlocks, ChatBlock{
								Kind:      BlockToolUse,
								ToolName:  block.Name,
								ToolID:    block.ID,
								ToolInput: inputStr,
							})
						}
					}
				}
				continue
			}

			// Top-level assistant event
			for _, block := range msg.Message.Content {
				switch block.Type {
				case "text":
					if block.Text != "" {
						blocks = append(blocks, ChatBlock{Kind: BlockText, Text: block.Text})
					}
				case "tool_use":
					inputStr := "{}"
					if len(block.Input) > 0 {
						inputStr = prettyJSON(block.Input)
					}
					cb := ChatBlock{
						Kind:      BlockToolUse,
						ToolName:  block.Name,
						ToolID:    block.ID,
						ToolInput: inputStr,
					}
					if block.Name == "Task" {
						cb.IsTask = true
						parseTaskInput(&cb, inputStr)
					}
					blocks = append(blocks, cb)
				}
			}

		case "content_block_start":
			var cbs struct {
				ContentBlock ContentBlock `json:"content_block"`
			}
			if json.Unmarshal([]byte(ev.Raw), &cbs) == nil && cbs.ContentBlock.Type == "tool_use" {
				inputStr := "{}"
				if len(cbs.ContentBlock.Input) > 0 {
					inputStr = prettyJSON(cbs.ContentBlock.Input)
				}
				cb := ChatBlock{
					Kind:      BlockToolUse,
					ToolName:  cbs.ContentBlock.Name,
					ToolID:    cbs.ContentBlock.ID,
					ToolInput: inputStr,
				}
				if cbs.ContentBlock.Name == "Task" {
					cb.IsTask = true
				}
				blocks = append(blocks, cb)
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
			if json.Unmarshal([]byte(ev.Raw), &msg) != nil {
				continue
			}

			if parentID != "" {
				// Subagent user event — route tool_result blocks to parent Task
				for _, block := range msg.Message.Content {
					if block.Type == "tool_result" {
						output := extractToolResultContent(block.Content, false)
						if idx := findTaskBlockIndex(blocks, parentID); idx >= 0 {
							blocks[idx].TaskSubBlocks = append(blocks[idx].TaskSubBlocks, ChatBlock{
								Kind:       BlockToolResult,
								ToolID:     block.ToolUseID,
								ToolOutput: output,
								IsError:    block.IsError,
							})
						}
					}
				}
				continue
			}

			// Top-level user event
			for _, block := range msg.Message.Content {
				if block.Type == "tool_result" {
					var output string
					if idx := findTaskBlockIndex(blocks, block.ToolUseID); idx >= 0 {
						// Task result — strip agentId block and parse metadata
						output = extractToolResultContent(block.Content, true)
						blocks[idx].TaskMeta = parseToolUseResult(ev.Raw)
					} else {
						output = extractToolResultContent(block.Content, false)
					}
					blocks = append(blocks, ChatBlock{
						Kind:       BlockToolResult,
						ToolID:     block.ToolUseID,
						ToolOutput: output,
						IsError:    block.IsError,
					})
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

// prettyJSON formats raw JSON with indentation, falling back to the raw string on error.
func prettyJSON(raw []byte) string {
	var pretty bytes.Buffer
	if json.Indent(&pretty, raw, "", "  ") == nil {
		return pretty.String()
	}
	return string(raw)
}

// streamDeltas holds extracted text, thinking, and tool input content from a content_block_delta event.
type streamDeltas struct {
	Text      string
	Thinking  string
	InputJSON string
}

// extractDeltas extracts text, thinking, and tool input content from a content_block_delta event.
// The Claude CLI wraps streaming events as:
//
//	{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello"}}}
//	{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"..."}}}
//	{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"..."}}}
func extractDeltas(raw string) streamDeltas {
	var wrapper struct {
		Event struct {
			Type  string `json:"type"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		} `json:"event"`
	}
	if json.Unmarshal([]byte(raw), &wrapper) == nil && wrapper.Event.Type == "content_block_delta" {
		switch wrapper.Event.Delta.Type {
		case "text_delta":
			return streamDeltas{Text: wrapper.Event.Delta.Text}
		case "thinking_delta":
			return streamDeltas{Thinking: wrapper.Event.Delta.Thinking}
		case "input_json_delta":
			return streamDeltas{InputJSON: wrapper.Event.Delta.PartialJSON}
		}
	}
	return streamDeltas{}
}

// wireLogConfig holds wire logging state, safe for concurrent access.
var wireLog struct {
	enabled atomic.Bool
	path    string
	once    sync.Once
}

// SetWireLogEnabled sets whether wire logging is active.
func SetWireLogEnabled(enabled bool) {
	wireLog.enabled.Store(enabled)
}

// WireLogPath returns the current wire log file path (empty if logging is disabled or no session yet).
func WireLogPath() string {
	return wireLog.path
}

// buildClaudeCmd constructs the exec.Cmd for a claude invocation with args and filtered env.
func buildClaudeCmd(prompt, sessionID string) *exec.Cmd {
	args := []string{"-p", "--output-format", "stream-json", "--verbose", "--include-partial-messages"}
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

	return cmd
}

// parseEventLine parses one NDJSON line, updating result/model/stopReason as needed.
// Returns the parsed StreamEvent (nil if line is not valid JSON) and any result unmarshal error.
func parseEventLine(line string, result *ClaudeResult, model *string, stopReason *string) (*StreamEvent, error) {
	var ev StreamEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return nil, nil
	}
	ev.Raw = line
	ev.ReceivedAt = time.Now()

	var resultErr error
	switch ev.Type {
	case "result":
		if err := json.Unmarshal([]byte(line), result); err != nil {
			resultErr = fmt.Errorf("result unmarshal: %w", err)
		}
	case "assistant":
		var msg struct {
			Message struct {
				Model      string `json:"model"`
				StopReason string `json:"stop_reason"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &msg) == nil {
			if msg.Message.Model != "" {
				*model = msg.Message.Model
			}
			if msg.Message.StopReason != "" {
				*stopReason = msg.Message.StopReason
			}
		}
	}

	return &ev, resultErr
}

// StreamClaude spawns claude in print mode and returns a channel that emits
// events incrementally. The channel is closed after the final StreamMsg{Done: true}.
func StreamClaude(prompt, sessionID string) (<-chan StreamMsg, *exec.Cmd, error) {
	cmd := buildClaudeCmd(prompt, sessionID)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("stdout pipe: %w", err)
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	startedAt := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start claude: %w", err)
	}

	// Open wire log file (one per app session, append across requests)
	var wl *os.File
	if wireLog.enabled.Load() {
		wireLog.once.Do(func() {
			wireLog.path = fmt.Sprintf("/tmp/flawdcode-%s.jsonl", startedAt.Format("20060102-150405"))
		})
		var wireLogErr error
		wl, wireLogErr = os.OpenFile(wireLog.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if wireLogErr != nil {
			wl = nil // non-fatal, just skip logging
		}
	}

	ch := make(chan StreamMsg, 64)

	go func() {
		defer close(ch)
		if wl != nil {
			defer wl.Close()
			// Log the outbound prompt as a synthetic event
			header, _ := json.Marshal(map[string]any{
				"_wire":      "request",
				"_ts":        startedAt.Format(time.RFC3339Nano),
				"prompt":     prompt,
				"session_id": sessionID,
				"command":    cmd.Args,
			})
			fmt.Fprintf(wl, "%s\n", header)
		}

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

			// Tee raw line to wire log
			if wl != nil {
				fmt.Fprintf(wl, "%s\n", line)
			}

			ev, resultErr := parseEventLine(line, &result, &model, &stopReason)
			if ev == nil {
				continue
			}
			if resultErr != nil && wl != nil {
				errJSON, _ := json.Marshal(map[string]any{
					"_wire": "error",
					"_ts":   time.Now().Format(time.RFC3339Nano),
					"error": resultErr.Error(),
				})
				fmt.Fprintf(wl, "%s\n", errJSON)
			}
			events = append(events, *ev)

			ch <- StreamMsg{Event: ev}
		}

		if scanErr := scanner.Err(); scanErr != nil {
			if wl != nil {
				errJSON, _ := json.Marshal(map[string]any{
					"_wire":  "error",
					"_ts":    time.Now().Format(time.RFC3339Nano),
					"error":  scanErr.Error(),
					"source": "scanner",
				})
				fmt.Fprintf(wl, "%s\n", errJSON)
			}
		}

		waitErr := cmd.Wait()

		resp := &ClaudeResponse{
			Command:    cmd.Args,
			Prompt:     prompt,
			Events:     events,
			Result:     result,
			Stderr:     stderr.String(),
			Model:      model,
			StopReason: stopReason,
			StartedAt:  startedAt,
		}

		// Log completion to wire log
		if wl != nil {
			trailer, _ := json.Marshal(map[string]any{
				"_wire":    "done",
				"_ts":      time.Now().Format(time.RFC3339Nano),
				"exit_err": fmt.Sprintf("%v", waitErr),
				"stderr":   stderr.String(),
				"model":    model,
				"stop":     stopReason,
			})
			fmt.Fprintf(wl, "%s\n", trailer)
		}

		if waitErr != nil {
			ch <- StreamMsg{
				Done: true,
				Err:  fmt.Errorf("claude: %w\nstderr: %s", waitErr, stderr.String()),
			}
		} else {
			ch <- StreamMsg{Done: true, Response: resp}
		}
	}()

	return ch, cmd, nil
}

// waitForStreamMsg returns a tea.Cmd that reads one message from the stream
// channel and converts it to the appropriate tea.Msg.
func waitForStreamMsg(ch <-chan StreamMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return ClaudeStreamDoneMsg{Err: fmt.Errorf("stream channel closed unexpectedly")}
		}
		if msg.Done {
			return ClaudeStreamDoneMsg{Response: msg.Response, Err: msg.Err}
		}
		deltas := extractDeltas(msg.Event.Raw)
		return ClaudeStreamChunkMsg{
			Event:          *msg.Event,
			TextDelta:      deltas.Text,
			ThinkingDelta:  deltas.Thinking,
			InputJSONDelta: deltas.InputJSON,
		}
	}
}

