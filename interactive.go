package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	expect "github.com/google/goexpect"
)

// ansiRe matches common ANSI escape sequences for stripping from PTY output.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\][^\x07]*\x07|\x1b\[\?[0-9;]*[hl]`)

// promptRe matches Claude Code's input prompt at end of output.
// Matches lines ending with common prompt characters.
var promptRe = regexp.MustCompile(`(?m)[❯>]\s*$`)

// InteractiveSession manages a persistent interactive Claude Code process via goexpect PTY.
// This is a proof of concept for replacing the print-mode (-p) subprocess approach
// with a single long-lived interactive session.
type InteractiveSession struct {
	exp   *expect.GExpect
	errCh <-chan error
	mu    sync.Mutex // serializes Send calls
}

// StartInteractive spawns claude in interactive mode and waits for the initial prompt.
func StartInteractive() (*InteractiveSession, error) {
	// Build environment: inherit all except CLAUDECODE (avoid recursion)
	env := os.Environ()
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, "CLAUDECODE=") {
			filtered = append(filtered, e)
		}
	}
	// Use dumb terminal to reduce escape sequences from the inner TUI
	filtered = append(filtered, "TERM=dumb")

	e, errCh, err := expect.SpawnWithArgs(
		[]string{"claude"},
		-1, // no global timeout
		expect.SetEnv(filtered),
	)
	if err != nil {
		return nil, fmt.Errorf("spawn claude: %w", err)
	}

	// Wait for initial prompt (up to 30s for startup)
	_, _, err = e.Expect(promptRe, 30*time.Second)
	if err != nil {
		e.Close()
		return nil, fmt.Errorf("waiting for initial prompt: %w", err)
	}

	return &InteractiveSession{exp: e, errCh: errCh}, nil
}

// SendPrompt sends a user prompt and returns a channel that streams response text chunks.
// The channel is closed when Claude's prompt reappears (indicating the response is complete).
// Only one SendPrompt may be active at a time.
func (s *InteractiveSession) SendPrompt(prompt string) (<-chan string, error) {
	s.mu.Lock()

	if err := s.exp.Send(prompt + "\n"); err != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("send prompt: %w", err)
	}

	ch := make(chan string, 64)
	go func() {
		defer s.mu.Unlock()
		defer close(ch)

		buf := make([]byte, 4096)
		var acc strings.Builder
		idleDeadline := time.Now().Add(5 * time.Minute)

		for time.Now().Before(idleDeadline) {
			n, err := s.exp.Read(buf)
			if n > 0 {
				raw := string(buf[:n])
				clean := stripANSI(raw)
				if clean != "" {
					ch <- clean
					acc.WriteString(clean)
				}
				// Reset idle deadline on any output
				idleDeadline = time.Now().Add(5 * time.Minute)
				// Check if prompt has reappeared (response complete)
				if promptRe.MatchString(acc.String()) {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	return ch, nil
}

// Close terminates the interactive session gracefully.
func (s *InteractiveSession) Close() error {
	// Try graceful exit first
	_ = s.exp.Send("/exit\n")
	time.Sleep(500 * time.Millisecond)
	return s.exp.Close()
}

// Err returns the error channel for the underlying process.
// It receives the process exit error (or nil) when claude terminates.
func (s *InteractiveSession) Err() <-chan error {
	return s.errCh
}

// stripANSI removes ANSI escape sequences from PTY output.
func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}
