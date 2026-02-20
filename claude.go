package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// StreamEvent is a single NDJSON line from claude's stdout.
type StreamEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype,omitempty"`
	Raw     string
}

// ClaudeResult is the final "result" event from claude.
type ClaudeResult struct {
	Type       string  `json:"type"`
	Subtype    string  `json:"subtype"`
	Result     string  `json:"result"`
	IsError    bool    `json:"is_error"`
	DurationMs int     `json:"duration_ms"`
	CostUSD    float64 `json:"cost_usd"`
	SessionID  string  `json:"session_id"`
}

// ClaudeResponse holds everything from a single claude invocation.
type ClaudeResponse struct {
	Command []string
	Prompt  string
	Events  []StreamEvent
	Result  ClaudeResult
	Stderr  string
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
// Output is stream-json (NDJSON).
func RunClaude(prompt string) (*ClaudeResponse, error) {
	cmd := exec.Command("claude", "-p",
		"--output-format", "stream-json",
		"--verbose",
		prompt,
	)

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

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude: %w", err)
	}

	// Read all NDJSON events from stdout
	var events []StreamEvent
	var result ClaudeResult
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
		events = append(events, ev)

		if ev.Type == "result" {
			json.Unmarshal([]byte(line), &result)
		}
	}

	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("claude: %w\nstderr: %s", err, stderr.String())
	}

	return &ClaudeResponse{
		Command: cmd.Args,
		Prompt:  prompt,
		Events:  events,
		Result:  result,
		Stderr:  stderr.String(),
	}, nil
}
