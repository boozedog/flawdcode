package main

import (
	"slices"
	"testing"
)

func TestExtractDeltas(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want streamDeltas
	}{
		{
			name: "text_delta",
			raw:  `{"event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello world"}}}`,
			want: streamDeltas{Text: "Hello world"},
		},
		{
			name: "thinking_delta",
			raw:  `{"event":{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"Let me think..."}}}`,
			want: streamDeltas{Thinking: "Let me think..."},
		},
		{
			name: "input_json_delta",
			raw:  `{"event":{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{\"key\":"}}}`,
			want: streamDeltas{InputJSON: `{"key":`},
		},
		{
			name: "invalid JSON",
			raw:  `not valid json at all`,
			want: streamDeltas{},
		},
		{
			name: "non-delta event type",
			raw:  `{"event":{"type":"content_block_start","content_block":{"type":"text"}}}`,
			want: streamDeltas{},
		},
		{
			name: "empty string",
			raw:  "",
			want: streamDeltas{},
		},
		{
			name: "delta with unknown type",
			raw:  `{"event":{"type":"content_block_delta","delta":{"type":"unknown_delta","text":"hello"}}}`,
			want: streamDeltas{},
		},
		{
			name: "text_delta with empty text",
			raw:  `{"event":{"type":"content_block_delta","delta":{"type":"text_delta","text":""}}}`,
			want: streamDeltas{Text: ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractDeltas(tt.raw)
			if got != tt.want {
				t.Errorf("extractDeltas() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestExtractParentToolUseID(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "with parent_tool_use_id",
			raw:  `{"type":"assistant","parent_tool_use_id":"toolu_abc123"}`,
			want: "toolu_abc123",
		},
		{
			name: "missing field",
			raw:  `{"type":"assistant"}`,
			want: "",
		},
		{
			name: "null value",
			raw:  `{"type":"assistant","parent_tool_use_id":null}`,
			want: "",
		},
		{
			name: "empty string value",
			raw:  `{"type":"assistant","parent_tool_use_id":""}`,
			want: "",
		},
		{
			name: "invalid JSON",
			raw:  `not json`,
			want: "",
		},
		{
			name: "empty string",
			raw:  "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractParentToolUseID(tt.raw)
			if got != tt.want {
				t.Errorf("extractParentToolUseID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseTaskInput(t *testing.T) {
	tests := []struct {
		name            string
		rawInput        string
		wantDescription string
		wantSubagent    string
		wantPrompt      string
	}{
		{
			name:            "valid input with all fields",
			rawInput:        `{"description":"Find bugs","subagent_type":"Bash","prompt":"Run tests"}`,
			wantDescription: "Find bugs",
			wantSubagent:    "Bash",
			wantPrompt:      "Run tests",
		},
		{
			name:            "missing optional fields",
			rawInput:        `{"description":"Search code"}`,
			wantDescription: "Search code",
			wantSubagent:    "",
			wantPrompt:      "",
		},
		{
			name:            "invalid JSON",
			rawInput:        `not json`,
			wantDescription: "",
			wantSubagent:    "",
			wantPrompt:      "",
		},
		{
			name:            "empty JSON object",
			rawInput:        `{}`,
			wantDescription: "",
			wantSubagent:    "",
			wantPrompt:      "",
		},
		{
			name:            "empty string",
			rawInput:        "",
			wantDescription: "",
			wantSubagent:    "",
			wantPrompt:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb := &ChatBlock{}
			parseTaskInput(cb, tt.rawInput)
			if cb.TaskDescription != tt.wantDescription {
				t.Errorf("TaskDescription = %q, want %q", cb.TaskDescription, tt.wantDescription)
			}
			if cb.TaskSubagentType != tt.wantSubagent {
				t.Errorf("TaskSubagentType = %q, want %q", cb.TaskSubagentType, tt.wantSubagent)
			}
			if cb.TaskPrompt != tt.wantPrompt {
				t.Errorf("TaskPrompt = %q, want %q", cb.TaskPrompt, tt.wantPrompt)
			}
		})
	}
}

func TestFindTaskBlockIndex(t *testing.T) {
	blocks := []ChatBlock{
		{Kind: BlockText, Text: "Hello"},
		{Kind: BlockToolUse, ToolName: "Bash", ToolID: "tool1"},
		{Kind: BlockToolUse, ToolName: "Task", ToolID: "task1", IsTask: true},
		{Kind: BlockToolResult, ToolID: "tool1"},
		{Kind: BlockToolUse, ToolName: "Task", ToolID: "task2", IsTask: true},
	}

	tests := []struct {
		name   string
		blocks []ChatBlock
		toolID string
		want   int
	}{
		{
			name:   "found first task",
			blocks: blocks,
			toolID: "task1",
			want:   2,
		},
		{
			name:   "found second task",
			blocks: blocks,
			toolID: "task2",
			want:   4,
		},
		{
			name:   "not found",
			blocks: blocks,
			toolID: "nonexistent",
			want:   -1,
		},
		{
			name:   "non-Task tool_use block with matching ID",
			blocks: blocks,
			toolID: "tool1",
			want:   -1,
		},
		{
			name:   "empty blocks",
			blocks: nil,
			toolID: "task1",
			want:   -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findTaskBlockIndex(tt.blocks, tt.toolID)
			if got != tt.want {
				t.Errorf("findTaskBlockIndex() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestParseToolUseResult(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want *TaskResultMeta
	}{
		{
			name: "valid result with all fields",
			raw:  `{"tool_use_result":{"agentId":"agent-xyz","totalDurationMs":5000,"totalTokens":1234,"totalToolUseCount":7}}`,
			want: &TaskResultMeta{
				AgentID:           "agent-xyz",
				TotalDurationMs:   5000,
				TotalTokens:       1234,
				TotalToolUseCount: 7,
			},
		},
		{
			name: "null tool_use_result",
			raw:  `{"tool_use_result":null}`,
			want: nil,
		},
		{
			name: "missing tool_use_result field",
			raw:  `{"type":"user"}`,
			want: nil,
		},
		{
			name: "invalid JSON",
			raw:  `not json`,
			want: nil,
		},
		{
			name: "empty string",
			raw:  "",
			want: nil,
		},
		{
			name: "partial fields",
			raw:  `{"tool_use_result":{"agentId":"a1","totalDurationMs":0,"totalTokens":0,"totalToolUseCount":0}}`,
			want: &TaskResultMeta{
				AgentID: "a1",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseToolUseResult(tt.raw)
			if tt.want == nil {
				if got != nil {
					t.Errorf("parseToolUseResult() = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("parseToolUseResult() = nil, want %+v", tt.want)
			}
			if *got != *tt.want {
				t.Errorf("parseToolUseResult() = %+v, want %+v", *got, *tt.want)
			}
		})
	}
}

func TestExtractToolResultContent(t *testing.T) {
	tests := []struct {
		name         string
		content      any
		stripAgentID bool
		want         string
	}{
		{
			name:         "string content",
			content:      "hello world",
			stripAgentID: false,
			want:         "hello world",
		},
		{
			name: "array of text blocks",
			content: []any{
				map[string]any{"type": "text", "text": "first"},
				map[string]any{"type": "text", "text": " second"},
			},
			stripAgentID: false,
			want:         "first second",
		},
		{
			name: "strip agentId block",
			content: []any{
				map[string]any{"type": "text", "text": "agentId:abc-123"},
				map[string]any{"type": "text", "text": "actual content"},
			},
			stripAgentID: true,
			want:         "actual content",
		},
		{
			name: "keep agentId when stripAgentID=false",
			content: []any{
				map[string]any{"type": "text", "text": "agentId:abc-123"},
				map[string]any{"type": "text", "text": "actual content"},
			},
			stripAgentID: false,
			want:         "agentId:abc-123actual content",
		},
		{
			name: "array with non-text items",
			content: []any{
				map[string]any{"type": "image", "url": "http://example.com"},
				map[string]any{"type": "text", "text": "visible"},
			},
			stripAgentID: false,
			want:         "visible",
		},
		{
			name:         "unknown type falls back to JSON",
			content:      42,
			stripAgentID: false,
			want:         "42",
		},
		{
			name:         "nil content",
			content:      nil,
			stripAgentID: false,
			want:         "null",
		},
		{
			name:         "empty array",
			content:      []any{},
			stripAgentID: false,
			want:         "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractToolResultContent(tt.content, tt.stripAgentID)
			if got != tt.want {
				t.Errorf("extractToolResultContent() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseEventLine(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		wantType   string
		wantNil    bool
		wantModel  string
		wantStop   string
		wantResult bool
		wantErr    bool
	}{
		{
			name:     "result event",
			line:     `{"type":"result","subtype":"success","result":"Done","duration_ms":1234,"total_cost_usd":0.05,"session_id":"sess-1","usage":{"input_tokens":100,"output_tokens":50}}`,
			wantType: "result",
			wantResult: true,
		},
		{
			name:      "assistant event with model",
			line:      `{"type":"assistant","message":{"model":"claude-sonnet-4-20250514","stop_reason":"end_turn","content":[]}}`,
			wantType:  "assistant",
			wantModel: "claude-sonnet-4-20250514",
			wantStop:  "end_turn",
		},
		{
			name:     "content_block_start",
			line:     `{"type":"content_block_start","content_block":{"type":"text","text":""}}`,
			wantType: "content_block_start",
		},
		{
			name:     "user event",
			line:     `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tool1","content":"ok"}]}}`,
			wantType: "user",
		},
		{
			name:    "invalid JSON",
			line:    `not json at all`,
			wantNil: true,
		},
		{
			name:    "empty string",
			line:    ``,
			wantNil: true,
		},
		{
			name:     "stream_event wrapper",
			line:     `{"type":"stream_event","event":{"type":"content_block_delta"}}`,
			wantType: "stream_event",
		},
		{
			name:    "result with bad JSON body",
			line:    `{"type":"result","usage":"bad"}`,
			wantType: "result",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result ClaudeResult
			var model, stopReason string
			ev, err := parseEventLine(tt.line, &result, &model, &stopReason)

			if tt.wantNil {
				if ev != nil {
					t.Fatalf("parseEventLine() returned non-nil event, want nil")
				}
				return
			}
			if ev == nil {
				t.Fatalf("parseEventLine() returned nil, want event type %q", tt.wantType)
			}
			if ev.Type != tt.wantType {
				t.Errorf("event type = %q, want %q", ev.Type, tt.wantType)
			}
			if ev.Raw != tt.line {
				t.Errorf("event Raw not preserved")
			}
			if tt.wantModel != "" && model != tt.wantModel {
				t.Errorf("model = %q, want %q", model, tt.wantModel)
			}
			if tt.wantStop != "" && stopReason != tt.wantStop {
				t.Errorf("stopReason = %q, want %q", stopReason, tt.wantStop)
			}
			if tt.wantResult && result.Type != "result" {
				t.Errorf("result.Type = %q, want 'result'", result.Type)
			}
			if tt.wantErr && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestExtractBlocks(t *testing.T) {
	t.Run("text and tool_use from assistant event", func(t *testing.T) {
		resp := &ClaudeResponse{
			Events: []StreamEvent{
				{Type: "assistant", Raw: `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello"},{"type":"tool_use","id":"tool1","name":"Bash","input":{"command":"ls"}}]}}`},
			},
		}
		blocks := resp.ExtractBlocks()
		if len(blocks) != 2 {
			t.Fatalf("got %d blocks, want 2", len(blocks))
		}
		if blocks[0].Kind != BlockText || blocks[0].Text != "Hello" {
			t.Errorf("block[0] = %+v, want text 'Hello'", blocks[0])
		}
		if blocks[1].Kind != BlockToolUse || blocks[1].ToolName != "Bash" || blocks[1].ToolID != "tool1" {
			t.Errorf("block[1] = %+v, want tool_use Bash/tool1", blocks[1])
		}
	})

	t.Run("tool_result from user event", func(t *testing.T) {
		resp := &ClaudeResponse{
			Events: []StreamEvent{
				{Type: "assistant", Raw: `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool1","name":"Bash","input":{}}]}}`},
				{Type: "user", Raw: `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tool1","content":"output here"}]}}`},
			},
		}
		blocks := resp.ExtractBlocks()
		if len(blocks) != 2 {
			t.Fatalf("got %d blocks, want 2", len(blocks))
		}
		if blocks[1].Kind != BlockToolResult || blocks[1].ToolOutput != "output here" {
			t.Errorf("block[1] = %+v, want tool_result with output", blocks[1])
		}
	})

	t.Run("Task tool with subagent routing", func(t *testing.T) {
		resp := &ClaudeResponse{
			Events: []StreamEvent{
				{Type: "assistant", Raw: `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"task1","name":"Task","input":{"description":"test","subagent_type":"Bash","prompt":"do stuff"}}]}}`},
				{Type: "assistant", Raw: `{"type":"assistant","parent_tool_use_id":"task1","message":{"content":[{"type":"tool_use","id":"sub1","name":"Read","input":{"file_path":"/tmp/test"}}]}}`},
				{Type: "user", Raw: `{"type":"user","parent_tool_use_id":"task1","message":{"content":[{"type":"tool_result","tool_use_id":"sub1","content":"file contents"}]}}`},
				{Type: "user", Raw: `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"task1","content":[{"type":"text","text":"agentId:abc"},{"type":"text","text":"task result"}]}]},"tool_use_result":{"agentId":"abc","totalDurationMs":5000,"totalTokens":100,"totalToolUseCount":3}}`},
			},
		}
		blocks := resp.ExtractBlocks()
		// Should have: 1 task tool_use + 1 tool_result for task
		if len(blocks) != 2 {
			t.Fatalf("got %d blocks, want 2", len(blocks))
		}
		task := blocks[0]
		if !task.IsTask {
			t.Error("expected IsTask=true")
		}
		if task.TaskDescription != "test" {
			t.Errorf("TaskDescription = %q, want 'test'", task.TaskDescription)
		}
		if len(task.TaskSubBlocks) != 2 {
			t.Fatalf("got %d sub-blocks, want 2", len(task.TaskSubBlocks))
		}
		if task.TaskSubBlocks[0].ToolName != "Read" {
			t.Errorf("sub-block[0] ToolName = %q, want 'Read'", task.TaskSubBlocks[0].ToolName)
		}
		if task.TaskSubBlocks[1].ToolOutput != "file contents" {
			t.Errorf("sub-block[1] ToolOutput = %q, want 'file contents'", task.TaskSubBlocks[1].ToolOutput)
		}
		if task.TaskMeta == nil || task.TaskMeta.AgentID != "abc" {
			t.Errorf("TaskMeta = %+v, want AgentID='abc'", task.TaskMeta)
		}
		// The task tool_result should strip agentId
		if blocks[1].ToolOutput != "task result" {
			t.Errorf("task result output = %q, want 'task result'", blocks[1].ToolOutput)
		}
	})

	t.Run("content_block_start event", func(t *testing.T) {
		resp := &ClaudeResponse{
			Events: []StreamEvent{
				{Type: "content_block_start", Raw: `{"type":"content_block_start","content_block":{"type":"tool_use","id":"cbs1","name":"Grep","input":{}}}`},
			},
		}
		blocks := resp.ExtractBlocks()
		if len(blocks) != 1 {
			t.Fatalf("got %d blocks, want 1", len(blocks))
		}
		if blocks[0].ToolName != "Grep" || blocks[0].ToolID != "cbs1" {
			t.Errorf("block = %+v, want Grep/cbs1", blocks[0])
		}
	})

	t.Run("empty events", func(t *testing.T) {
		resp := &ClaudeResponse{}
		blocks := resp.ExtractBlocks()
		if len(blocks) != 0 {
			t.Errorf("got %d blocks, want 0", len(blocks))
		}
	})

	t.Run("error tool result", func(t *testing.T) {
		resp := &ClaudeResponse{
			Events: []StreamEvent{
				{Type: "assistant", Raw: `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{}}]}}`},
				{Type: "user", Raw: `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"permission denied","is_error":true}]}}`},
			},
		}
		blocks := resp.ExtractBlocks()
		if len(blocks) != 2 {
			t.Fatalf("got %d blocks, want 2", len(blocks))
		}
		if !blocks[1].IsError {
			t.Error("expected IsError=true on tool_result")
		}
	})
}

func TestPermissionModeNext(t *testing.T) {
	tests := []struct {
		name string
		mode PermissionMode
		want PermissionMode
	}{
		{"plan -> acceptEdits", PermPlan, PermAcceptEdits},
		{"acceptEdits -> bypassPermissions", PermAcceptEdits, PermBypassPermissions},
		{"bypassPermissions -> dontAsk", PermBypassPermissions, PermDontAsk},
		{"dontAsk -> plan (wrap)", PermDontAsk, PermPlan},
		{"unknown -> plan (fallback)", PermissionMode("unknown"), PermPlan},
		{"empty -> plan (fallback)", PermissionMode(""), PermPlan},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.mode.Next()
			if got != tt.want {
				t.Errorf("Next() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPermissionModeShort(t *testing.T) {
	tests := []struct {
		mode PermissionMode
		want string
	}{
		{PermPlan, "plan"},
		{PermAcceptEdits, "edits"},
		{PermBypassPermissions, "bypass"},
		{PermDontAsk, "yolo"},
		{PermissionMode("custom"), "custom"},
	}
	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			got := tt.mode.Short()
			if got != tt.want {
				t.Errorf("Short() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildClaudeCmdPermissionMode(t *testing.T) {
	t.Run("includes permission mode", func(t *testing.T) {
		cmd := buildClaudeCmd("hello", "", PermPlan)
		args := cmd.Args[1:] // skip "claude" binary
		idx := slices.Index(args, "--permission-mode")
		if idx < 0 || idx+1 >= len(args) {
			t.Fatalf("--permission-mode not found in args: %v", args)
		}
		if args[idx+1] != "plan" {
			t.Errorf("permission mode = %q, want 'plan'", args[idx+1])
		}
	})

	t.Run("omits when empty", func(t *testing.T) {
		cmd := buildClaudeCmd("hello", "", "")
		args := cmd.Args[1:]
		if slices.Contains(args, "--permission-mode") {
			t.Errorf("--permission-mode should not be present for empty mode, args: %v", args)
		}
	})

	t.Run("includes session ID", func(t *testing.T) {
		cmd := buildClaudeCmd("hello", "sess-123", PermAcceptEdits)
		args := cmd.Args[1:]
		if !slices.Contains(args, "--resume") {
			t.Fatalf("--resume not found in args: %v", args)
		}
		idx := slices.Index(args, "--resume")
		if args[idx+1] != "sess-123" {
			t.Errorf("session ID = %q, want 'sess-123'", args[idx+1])
		}
		// Also check permission mode is there
		pmIdx := slices.Index(args, "--permission-mode")
		if pmIdx < 0 || args[pmIdx+1] != "acceptEdits" {
			t.Errorf("permission mode not correct in args: %v", args)
		}
	})
}

func TestPrettyJSON(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
		want string
	}{
		{
			name: "valid JSON",
			raw:  []byte(`{"key":"value"}`),
			want: "{\n  \"key\": \"value\"\n}",
		},
		{
			name: "invalid JSON",
			raw:  []byte(`not json`),
			want: "not json",
		},
		{
			name: "empty object",
			raw:  []byte(`{}`),
			want: "{}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := prettyJSON(tt.raw)
			if got != tt.want {
				t.Errorf("prettyJSON(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
