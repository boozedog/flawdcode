package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

// parseStreamBlock handles content_block_start, user/tool_result, and subagent
// events from streaming. Subagent events (parent_tool_use_id set) are routed
// into their parent Task block's TaskSubBlocks.
func (m *ChatModel) parseStreamBlock(entry *chatEntry, ev StreamEvent) {
	// Route subagent events to parent Task block
	parentID := extractParentToolUseID(ev.Raw)
	if parentID != "" {
		taskIdx := findTaskBlockIndex(entry.blocks, parentID)
		if taskIdx < 0 {
			return
		}
		// Lazily parse Task input fields on first subagent event
		if entry.blocks[taskIdx].TaskDescription == "" && entry.blocks[taskIdx].ToolInput != "" {
			parseTaskInput(&entry.blocks[taskIdx], entry.blocks[taskIdx].ToolInput)
		}

		switch ev.Type {
		case "assistant":
			var msg struct {
				Message struct {
					Content []ContentBlock `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal([]byte(ev.Raw), &msg) == nil {
				for _, block := range msg.Message.Content {
					if block.Type == "tool_use" {
						inputStr := "{}"
						if len(block.Input) > 0 {
							inputStr = prettyJSON(block.Input)
						}
						entry.blocks[taskIdx].TaskSubBlocks = append(entry.blocks[taskIdx].TaskSubBlocks, ChatBlock{
							Kind:      BlockToolUse,
							ToolName:  block.Name,
							ToolID:    block.ID,
							ToolInput: inputStr,
						})
					}
				}
			}
		case "user":
			var userMsg struct {
				Message struct {
					Content []struct {
						Type      string `json:"type"`
						ToolUseID string `json:"tool_use_id"`
						Content   any    `json:"content"`
						IsError   bool   `json:"is_error"`
					} `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal([]byte(ev.Raw), &userMsg) == nil {
				for _, block := range userMsg.Message.Content {
					if block.Type == "tool_result" {
						output := extractToolResultContent(block.Content, false)
						entry.blocks[taskIdx].TaskSubBlocks = append(entry.blocks[taskIdx].TaskSubBlocks, ChatBlock{
							Kind:       BlockToolResult,
							ToolID:     block.ToolUseID,
							ToolOutput: output,
							IsError:    block.IsError,
						})
					}
				}
			}
		}
		return
	}

	// Normal (non-subagent) event processing
	switch ev.Type {
	case "content_block_start":
		m.addContentBlock(entry, ev.Raw)
	case "stream_event":
		// The Claude CLI wraps API events inside {"type":"stream_event","event":{...}}
		var wrapper struct {
			Event struct {
				Type         string       `json:"type"`
				ContentBlock ContentBlock `json:"content_block"`
			} `json:"event"`
		}
		if json.Unmarshal([]byte(ev.Raw), &wrapper) == nil && wrapper.Event.Type == "content_block_start" {
			switch wrapper.Event.ContentBlock.Type {
			case "text":
				entry.blocks = append(entry.blocks, ChatBlock{Kind: BlockText})
			case "tool_use":
				cb := ChatBlock{
					Kind:     BlockToolUse,
					ToolName: wrapper.Event.ContentBlock.Name,
					ToolID:   wrapper.Event.ContentBlock.ID,
				}
				if wrapper.Event.ContentBlock.Name == "Task" {
					cb.IsTask = true
				}
				entry.blocks = append(entry.blocks, cb)
			}
		}
	case "user":
		var userMsg struct {
			Message struct {
				Content []struct {
					Type      string `json:"type"`
					ToolUseID string `json:"tool_use_id"`
					Content   any    `json:"content"`
					IsError   bool   `json:"is_error"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(ev.Raw), &userMsg) == nil {
			for _, block := range userMsg.Message.Content {
				if block.Type == "tool_result" {
					var output string
					if taskIdx := findTaskBlockIndex(entry.blocks, block.ToolUseID); taskIdx >= 0 {
						// Task result — strip agentId block and parse metadata
						output = extractToolResultContent(block.Content, true)
						entry.blocks[taskIdx].TaskMeta = parseToolUseResult(ev.Raw)
					} else {
						output = extractToolResultContent(block.Content, false)
					}
					entry.blocks = append(entry.blocks, ChatBlock{
						Kind:       BlockToolResult,
						ToolID:     block.ToolUseID,
						ToolOutput: output,
						IsError:    block.IsError,
					})
				}
			}
		}
	}
}

// addContentBlock parses a top-level content_block_start event and adds the block.
func (m *ChatModel) addContentBlock(entry *chatEntry, raw string) {
	var cbs struct {
		ContentBlock ContentBlock `json:"content_block"`
	}
	if json.Unmarshal([]byte(raw), &cbs) == nil {
		switch cbs.ContentBlock.Type {
		case "text":
			entry.blocks = append(entry.blocks, ChatBlock{Kind: BlockText})
		case "tool_use":
			cb := ChatBlock{
				Kind:     BlockToolUse,
				ToolName: cbs.ContentBlock.Name,
				ToolID:   cbs.ContentBlock.ID,
			}
			if cbs.ContentBlock.Name == "Task" {
				cb.IsTask = true
			}
			entry.blocks = append(entry.blocks, cb)
		}
	}
}

// lastBlockIndex returns the index of the last block with the given kind, or -1.
func lastBlockIndex(blocks []ChatBlock, kind BlockKind) int {
	for i := len(blocks) - 1; i >= 0; i-- {
		if blocks[i].Kind == kind {
			return i
		}
	}
	return -1
}

// truncateRunes truncates s to maxLen grapheme clusters, appending "..." if truncated.
func truncateRunes(s string, maxLen int) string {
	if maxLen <= 0 {
		return s
	}
	// Count grapheme clusters and collect their byte boundaries
	gr := uniseg.NewGraphemes(s)
	var boundaries []int
	boundaries = append(boundaries, 0)
	for gr.Next() {
		_, to := gr.Positions()
		boundaries = append(boundaries, to)
	}
	count := len(boundaries) - 1
	if count <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:boundaries[maxLen]]
	}
	return s[:boundaries[maxLen-3]] + "..."
}

func formatTokens(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

// wrapText breaks text into lines of at most width runes, preserving
// existing newlines and preferring word boundaries.
func wrapText(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	// Split on existing newlines first
	paragraphs := strings.Split(text, "\n")
	var lines []string
	for _, para := range paragraphs {
		if utf8.RuneCountInString(para) <= width {
			lines = append(lines, para)
			continue
		}
		// Wrap this paragraph
		remaining := para
		for utf8.RuneCountInString(remaining) > width {
			// Get the rune-bounded prefix
			prefix := string([]rune(remaining)[:width])
			breakAt := strings.LastIndex(prefix, " ")
			if breakAt <= 0 {
				// No word boundary; hard-break at width runes
				lines = append(lines, prefix)
				remaining = remaining[len(prefix):]
			} else {
				lines = append(lines, remaining[:breakAt])
				remaining = strings.TrimLeft(remaining[breakAt:], " ")
			}
		}
		if remaining != "" {
			lines = append(lines, remaining)
		}
	}
	return lines
}

// firstLine returns the first non-empty line of s, truncated to maxLen.
func firstLine(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	return truncateRunes(s, maxLen)
}

// toolInputSummary extracts a meaningful one-line summary from a tool's JSON input.
func toolInputSummary(toolName, jsonInput string, maxLen int) string {
	var fields map[string]any
	if json.Unmarshal([]byte(jsonInput), &fields) != nil {
		return firstLine(jsonInput, maxLen)
	}

	// Pick the most meaningful field per tool type
	var summary string
	switch toolName {
	case "Bash":
		if cmd, ok := fields["command"].(string); ok {
			summary = cmd
		}
	case "Read":
		if fp, ok := fields["file_path"].(string); ok {
			summary = fp
		}
	case "Write":
		if fp, ok := fields["file_path"].(string); ok {
			summary = fp
		}
	case "Edit":
		if fp, ok := fields["file_path"].(string); ok {
			summary = fp
		}
	case "Glob":
		if p, ok := fields["pattern"].(string); ok {
			summary = p
			if path, ok := fields["path"].(string); ok {
				summary = path + "/" + p
			}
		}
	case "Grep":
		if p, ok := fields["pattern"].(string); ok {
			summary = p
		}
	case "WebFetch":
		if u, ok := fields["url"].(string); ok {
			summary = u
		}
	default:
		// Try common field names
		for _, key := range []string{"command", "file_path", "path", "pattern", "query", "url", "prompt"} {
			if v, ok := fields[key].(string); ok && v != "" {
				summary = v
				break
			}
		}
	}

	if summary == "" {
		return firstLine(jsonInput, maxLen)
	}

	return firstLine(summary, maxLen)
}

// cleanToolOutput cleans up tool output for display, stripping XML error tags.
func cleanToolOutput(s string) string {
	s = strings.TrimSpace(s)
	// Strip <tool_use_error>...</tool_use_error> XML tags
	if strings.HasPrefix(s, "<tool_use_error>") {
		s = strings.TrimPrefix(s, "<tool_use_error>")
		s = strings.TrimSuffix(s, "</tool_use_error>")
		s = strings.TrimSpace(s)
	}
	// Strip cat-n style prefix from first line (e.g., "     1→")
	if idx := strings.Index(s, "→"); idx >= 0 && idx < 12 {
		prefix := strings.TrimSpace(s[:idx])
		if _, err := fmt.Sscanf(prefix, "%d", new(int)); err == nil {
			s = strings.TrimSpace(s[idx+len("→"):])
		}
	}
	return s
}
