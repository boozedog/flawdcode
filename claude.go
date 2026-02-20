package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
)

// ClaudeProcess manages the claude subprocess lifecycle.
type ClaudeProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr bytes.Buffer
	mu     sync.Mutex
	msgCh  chan tea.Msg
}

// NewClaudeProcess creates a new ClaudeProcess.
func NewClaudeProcess() *ClaudeProcess {
	return &ClaudeProcess{
		msgCh: make(chan tea.Msg, 256),
	}
}

// Start spawns the claude subprocess.
func (c *ClaudeProcess) Start() error {
	c.cmd = exec.Command("claude", "-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
	)

	// Filter out CLAUDECODE env var so claude can launch as a subprocess
	env := os.Environ()
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, "CLAUDECODE=") {
			filtered = append(filtered, e)
		}
	}
	c.cmd.Env = filtered
	c.cmd.Stderr = &c.stderr

	var err error
	c.stdin, err = c.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	c.stdout, err = c.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	if err := c.cmd.Start(); err != nil {
		return fmt.Errorf("start claude: %w", err)
	}

	return nil
}

// ReadLoop reads stdout line-by-line and sends parsed messages to the channel.
// Run this in a goroutine.
func (c *ClaudeProcess) ReadLoop() {
	scanner := bufio.NewScanner(c.stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var env StreamMsg
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			continue
		}
		c.msgCh <- ClaudeOutputMsg{
			StreamMsg: env,
			Line:      line,
		}
	}

	err := c.cmd.Wait()
	if err != nil && c.stderr.Len() > 0 {
		err = fmt.Errorf("%w: %s", err, strings.TrimSpace(c.stderr.String()))
	}
	c.msgCh <- ClaudeExitMsg{Err: err}
}

// WaitForOutput returns a tea.Cmd that blocks until the next message from Claude.
func (c *ClaudeProcess) WaitForOutput() tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-c.msgCh
		if !ok {
			return ClaudeExitMsg{Err: nil}
		}
		return msg
	}
}

// SendMessage writes a user message to Claude's stdin as NDJSON.
func (c *ClaudeProcess) SendMessage(text string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	msg := UserMessageInput{
		Type: "user_message",
		Content: UserMessageContent{
			Type: "text",
			Text: text,
		},
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return "", err
	}
	data = append(data, '\n')

	_, err = c.stdin.Write(data)
	return string(data[:len(data)-1]), err
}

// Close kills the subprocess.
func (c *ClaudeProcess) Close() {
	if c.stdin != nil {
		c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		c.cmd.Process.Kill()
	}
}
