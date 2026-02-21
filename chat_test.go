package main

import (
	"testing"
)

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		name string
		n    int
		want string
	}{
		{name: "zero", n: 0, want: "0"},
		{name: "below 1k", n: 999, want: "999"},
		{name: "exactly 1k", n: 1000, want: "1.0k"},
		{name: "1.5k", n: 1500, want: "1.5k"},
		{name: "just below 1M", n: 999999, want: "1000.0k"},
		{name: "exactly 1M", n: 1000000, want: "1.0M"},
		{name: "1.5M", n: 1500000, want: "1.5M"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatTokens(tt.n)
			if got != tt.want {
				t.Errorf("formatTokens(%d) = %q, want %q", tt.n, got, tt.want)
			}
		})
	}
}

func TestFirstLine(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		maxLen int
		want   string
	}{
		{name: "single line short", s: "hello", maxLen: 10, want: "hello"},
		{name: "multiline", s: "first\nsecond\nthird", maxLen: 100, want: "first"},
		{name: "empty string", s: "", maxLen: 10, want: ""},
		{name: "whitespace only", s: "   \n  ", maxLen: 10, want: ""},
		{name: "truncation", s: "a very long line", maxLen: 10, want: "a very ..."},
		{name: "leading whitespace", s: "  hello\nworld", maxLen: 100, want: "hello"},
		{name: "trailing whitespace on first line", s: "hello  \nworld", maxLen: 100, want: "hello"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := firstLine(tt.s, tt.maxLen)
			if got != tt.want {
				t.Errorf("firstLine(%q, %d) = %q, want %q", tt.s, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		maxLen int
		want   string
	}{
		{name: "short ASCII", s: "hello", maxLen: 10, want: "hello"},
		{name: "exact length", s: "hello", maxLen: 5, want: "hello"},
		{name: "truncate ASCII", s: "hello world", maxLen: 8, want: "hello..."},
		{name: "maxLen=0 returns original", s: "hello", maxLen: 0, want: "hello"},
		{name: "maxLen<=3 no ellipsis", s: "hello", maxLen: 3, want: "hel"},
		{name: "maxLen=1", s: "hello", maxLen: 1, want: "h"},
		{name: "negative maxLen returns original", s: "hello", maxLen: -1, want: "hello"},
		{name: "emoji multi-byte", s: "🎉🎉🎉🎉🎉", maxLen: 4, want: "🎉..."},
		{name: "emoji exact", s: "🎉🎉🎉", maxLen: 3, want: "🎉🎉🎉"},
		{name: "CJK characters", s: "你好世界测试", maxLen: 5, want: "你好..."},
		{name: "CJK exact length", s: "你好世", maxLen: 3, want: "你好世"},
		{name: "mixed ASCII and emoji", s: "hi🎉bye", maxLen: 5, want: "hi..."},
		{name: "empty string", s: "", maxLen: 10, want: ""},
		{
			name:   "combining characters",
			s:      "e\u0301e\u0301e\u0301e\u0301e\u0301", // é as e + combining accent (5 graphemes)
			maxLen: 4,
			want:   "e\u0301...",
		},
		{
			name:   "combining characters exact",
			s:      "e\u0301e\u0301e\u0301", // 3 graphemes
			maxLen: 3,
			want:   "e\u0301e\u0301e\u0301",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateRunes(tt.s, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncateRunes(%q, %d) = %q, want %q", tt.s, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestToolInputSummary(t *testing.T) {
	tests := []struct {
		name      string
		toolName  string
		jsonInput string
		maxLen    int
		want      string
	}{
		{
			name:      "Bash command",
			toolName:  "Bash",
			jsonInput: `{"command":"go test ./...","description":"Run tests"}`,
			maxLen:    50,
			want:      "go test ./...",
		},
		{
			name:      "Read file_path",
			toolName:  "Read",
			jsonInput: `{"file_path":"/src/main.go"}`,
			maxLen:    50,
			want:      "/src/main.go",
		},
		{
			name:      "Write file_path",
			toolName:  "Write",
			jsonInput: `{"file_path":"/src/test.go","content":"package main"}`,
			maxLen:    50,
			want:      "/src/test.go",
		},
		{
			name:      "Glob with path",
			toolName:  "Glob",
			jsonInput: `{"pattern":"*.go","path":"/src"}`,
			maxLen:    50,
			want:      "/src/*.go",
		},
		{
			name:      "Glob without path",
			toolName:  "Glob",
			jsonInput: `{"pattern":"**/*.ts"}`,
			maxLen:    50,
			want:      "**/*.ts",
		},
		{
			name:      "Grep pattern",
			toolName:  "Grep",
			jsonInput: `{"pattern":"func Test","path":"/src"}`,
			maxLen:    50,
			want:      "func Test",
		},
		{
			name:      "unknown tool with common field",
			toolName:  "CustomTool",
			jsonInput: `{"command":"do stuff","extra":"data"}`,
			maxLen:    50,
			want:      "do stuff",
		},
		{
			name:      "unknown tool no common field",
			toolName:  "CustomTool",
			jsonInput: `{"foo":"bar"}`,
			maxLen:    50,
			want:      `{"foo":"bar"}`,
		},
		{
			name:      "invalid JSON",
			toolName:  "Bash",
			jsonInput: `not json`,
			maxLen:    50,
			want:      "not json",
		},
		{
			name:      "truncation of long summary",
			toolName:  "Bash",
			jsonInput: `{"command":"very long command that should be truncated"}`,
			maxLen:    15,
			want:      "very long co...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toolInputSummary(tt.toolName, tt.jsonInput, tt.maxLen)
			if got != tt.want {
				t.Errorf("toolInputSummary(%q, %q, %d) = %q, want %q",
					tt.toolName, tt.jsonInput, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestCleanToolOutput(t *testing.T) {
	tests := []struct {
		name string
		s    string
		want string
	}{
		{
			name: "plain text",
			s:    "hello world",
			want: "hello world",
		},
		{
			name: "tool_use_error tags",
			s:    "<tool_use_error>Permission denied</tool_use_error>",
			want: "Permission denied",
		},
		{
			name: "cat-n prefix",
			s:    "     1→package main",
			want: "package main",
		},
		{
			name: "cat-n prefix with higher number",
			s:    "   42→func main() {",
			want: "func main() {",
		},
		{
			name: "no cat-n prefix (arrow too far)",
			s:    "not a cat-n prefix → something",
			want: "not a cat-n prefix → something",
		},
		{
			name: "empty string",
			s:    "",
			want: "",
		},
		{
			name: "whitespace only",
			s:    "   \n  ",
			want: "",
		},
		{
			name: "tool_use_error with whitespace",
			s:    "  <tool_use_error>  error message  </tool_use_error>  ",
			want: "error message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanToolOutput(tt.s)
			if got != tt.want {
				t.Errorf("cleanToolOutput(%q) = %q, want %q", tt.s, got, tt.want)
			}
		})
	}
}

func TestStripANSI(t *testing.T) {
	tests := []struct {
		name string
		s    string
		want string
	}{
		{name: "no escapes", s: "hello world", want: "hello world"},
		{name: "empty", s: "", want: ""},
		{name: "color code", s: "\x1b[31mred\x1b[0m", want: "red"},
		{name: "bold", s: "\x1b[1mbold\x1b[0m", want: "bold"},
		{name: "cursor movement", s: "\x1b[2Jhello", want: "hello"},
		{name: "OSC sequence", s: "\x1b]0;title\x07text", want: "text"},
		{name: "private mode", s: "\x1b[?25lhidden\x1b[?25h", want: "hidden"},
		{name: "multiple escapes", s: "\x1b[1m\x1b[31mbold red\x1b[0m normal", want: "bold red normal"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripANSI(tt.s)
			if got != tt.want {
				t.Errorf("stripANSI(%q) = %q, want %q", tt.s, got, tt.want)
			}
		})
	}
}

func TestWrapText(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		width int
		want  []string
	}{
		{
			name:  "short text fits",
			text:  "hello",
			width: 20,
			want:  []string{"hello"},
		},
		{
			name:  "long text wraps at word boundary",
			text:  "hello world foo bar",
			width: 11,
			want:  []string{"hello", "world foo", "bar"},
		},
		{
			name:  "existing newlines preserved",
			text:  "line one\nline two",
			width: 50,
			want:  []string{"line one", "line two"},
		},
		{
			name:  "width=0 returns single element",
			text:  "hello world",
			width: 0,
			want:  []string{"hello world"},
		},
		{
			name:  "long word exceeds width",
			text:  "superlongword more",
			width: 5,
			want:  []string{"super", "longw", "ord", "more"},
		},
		{
			name:  "empty string",
			text:  "",
			width: 20,
			want:  []string{""},
		},
		{
			name:  "exactly at width",
			text:  "hello",
			width: 5,
			want:  []string{"hello"},
		},
		{
			name:  "mixed newlines and wrapping",
			text:  "short\nthis line is quite long and needs wrapping",
			width: 20,
			want:  []string{"short", "this line is quite", "long and needs", "wrapping"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapText(tt.text, tt.width)
			if len(got) != len(tt.want) {
				t.Fatalf("wrapText(%q, %d) returned %d lines, want %d\ngot:  %q\nwant: %q",
					tt.text, tt.width, len(got), len(tt.want), got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("wrapText(%q, %d)[%d] = %q, want %q",
						tt.text, tt.width, i, got[i], tt.want[i])
				}
			}
		})
	}
}
