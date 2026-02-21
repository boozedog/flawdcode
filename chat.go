package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
)

type chatEntry struct {
	role       string      // "user", "assistant", or "error"
	text       string      // plain text for user/error; fallback text for assistant
	thinking   string      // thinking text (persisted after streaming)
	blocks     []ChatBlock // parsed content blocks for assistant responses
	result     ClaudeResult
	model      string
	stopReason string
	hasResult      bool
	cacheReadTok   int
	durationMs     int
	durationAPIMs  int
	streaming      bool   // true while being streamed
	streamThinking string // accumulated thinking text during streaming
	streamText     string // accumulated raw text during streaming
}

// ChatModel is the chat tab: viewport (history) + textarea (input) + glamour rendering.
type ChatModel struct {
	viewport      viewport.Model
	textarea      textarea.Model
	entries       []chatEntry
	width         int
	height        int
	renderer      *glamour.TermRenderer
	sessionID     string         // persist session for --resume
	streamCh      <-chan StreamMsg // current stream channel
	cachedContent string          // rendered content of all finalized entries
	scrollMode    bool            // when true, keys go to viewport instead of textarea

	// Session-level cumulative stats for status line
	totalCost      float64
	totalInputTok  int
	totalOutputTok int
	totalRequests  int
	lastModel      string
	lastCost       float64
	lastInputTok   int
	lastOutputTok  int
	lastCacheRead  int
	lastDurationMs int
	lastAPIMs      int

	// Init event data (shown as startup banner)
	initModel      string
	initVersion    string
	initNumTools   int
	initPermMode   string
	initPlugins    []string
	initReceived   bool

	// Rate limit tracking
	rateLimitStatus    string // "allowed", "throttled"
	rateLimitResetsAt  time.Time
	rateLimitOverage   string // "allowed", "throttled"
	rateLimitIsOverage bool
}

// NewChatModel creates a new chat tab model.
func NewChatModel() ChatModel {
	ta := textarea.New()
	ta.Placeholder = "Type a message..."
	ta.SetHeight(3)
	ta.CharLimit = 0

	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	vp.SoftWrap = true
	vp.KeyMap.Left = key.NewBinding(key.WithDisabled())
	vp.KeyMap.Right = key.NewBinding(key.WithDisabled())

	r, _ := glamour.NewTermRenderer(
		glamour.WithStandardStyle(styles.DarkStyle),
		glamour.WithWordWrap(76),
	)

	return ChatModel{
		viewport: vp,
		textarea: ta,
		renderer: r,
	}
}

// Init returns the initial command (focus textarea).
func (m ChatModel) Init() tea.Cmd {
	return m.textarea.Focus()
}

// Update handles messages for the chat tab.
func (m ChatModel) Update(msg tea.Msg) (ChatModel, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		// Scroll mode toggle
		if msg.String() == "esc" {
			m.scrollMode = !m.scrollMode
			if m.scrollMode {
				m.textarea.Blur()
			} else {
				cmds = append(cmds, m.textarea.Focus())
			}
			return m, tea.Batch(cmds...)
		}
		if m.scrollMode {
			// Exit scroll mode on i or enter
			if msg.String() == "i" || msg.String() == "enter" {
				m.scrollMode = false
				return m, m.textarea.Focus()
			}
			// Route all other keys to viewport
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}

		if msg.String() == "shift+enter" {
			m.textarea.InsertRune('\n')
			return m, nil
		}
		if msg.String() == "enter" {
			text := strings.TrimSpace(m.textarea.Value())
			if text != "" && m.streamCh == nil {
				m.textarea.Reset()
				m.entries = append(m.entries, chatEntry{role: "user", text: text})
				m.refreshViewport()

				sid := m.sessionID
				cmds = append(cmds, func() tea.Msg {
					ch, err := StreamClaude(text, sid)
					if err != nil {
						return ClaudeStreamDoneMsg{Prompt: text, Err: err}
					}
					return ClaudeStreamStartMsg{Prompt: text, Ch: ch}
				})
			}
			return m, tea.Batch(cmds...)
		}

	case ClaudeStreamStartMsg:
		m.streamCh = msg.Ch
		m.entries = append(m.entries, chatEntry{
			role:      "assistant",
			streaming: true,
		})
		m.refreshStreamingViewport()
		return m, waitForStreamMsg(msg.Ch)

	case ClaudeStreamChunkMsg:
		// Extract init event data for startup banner
		if msg.Event.Type == "system" && msg.Event.Subtype == "init" && !m.initReceived {
			m.parseInitEvent(msg.Event)
		}
		// Extract rate limit info
		if msg.Event.Type == "rate_limit_event" {
			m.parseRateLimitEvent(msg.Event)
		}

		if len(m.entries) > 0 {
			last := &m.entries[len(m.entries)-1]
			if last.streaming {
				// Build blocks incrementally from stream events
				m.parseStreamBlock(last, msg.Event)

				// Apply deltas to the appropriate block
				if msg.ThinkingDelta != "" {
					last.streamThinking += msg.ThinkingDelta
				}
				if msg.TextDelta != "" {
					last.streamText += msg.TextDelta
					// Append to last text block, auto-create if none
					if idx := lastBlockIndex(last.blocks, "text"); idx >= 0 {
						last.blocks[idx].Text += msg.TextDelta
					} else {
						last.blocks = append(last.blocks, ChatBlock{Kind: "text", Text: msg.TextDelta})
					}
				}
				if msg.InputJSONDelta != "" {
					if idx := lastBlockIndex(last.blocks, "tool_use"); idx >= 0 {
						last.blocks[idx].ToolInput += msg.InputJSONDelta
					}
				}
				m.refreshStreamingViewport()
			}
		}
		if m.streamCh != nil {
			return m, waitForStreamMsg(m.streamCh)
		}
		return m, nil

	case ClaudeStreamDoneMsg:
		m.streamCh = nil
		if msg.Err != nil {
			// Replace streaming entry with error
			if len(m.entries) > 0 && m.entries[len(m.entries)-1].streaming {
				m.entries[len(m.entries)-1] = chatEntry{role: "error", text: msg.Err.Error()}
			} else {
				m.entries = append(m.entries, chatEntry{role: "error", text: msg.Err.Error()})
			}
		} else if msg.Response != nil {
			if msg.Response.Result.SessionID != "" {
				m.sessionID = msg.Response.Result.SessionID
			}
			// Finalize the streaming entry in place — blocks were built incrementally
			if len(m.entries) > 0 && m.entries[len(m.entries)-1].streaming {
				last := &m.entries[len(m.entries)-1]
				last.streaming = false
				last.text = last.streamText
				last.thinking = last.streamThinking
				last.result = msg.Response.Result
				last.model = msg.Response.Model
				last.stopReason = msg.Response.StopReason
				last.hasResult = true
				last.cacheReadTok = msg.Response.Result.Usage.CacheReadInputTokens
				last.durationMs = msg.Response.Result.DurationMs
				last.durationAPIMs = msg.Response.Result.DurationAPIMs

				// Pretty-print tool input JSON and parse Task inputs now that streaming is done
				for i := range last.blocks {
					if last.blocks[i].Kind == "tool_use" && last.blocks[i].ToolInput != "" {
						var pretty bytes.Buffer
						if json.Indent(&pretty, []byte(last.blocks[i].ToolInput), "", "  ") == nil {
							last.blocks[i].ToolInput = pretty.String()
						}
						if last.blocks[i].IsTask {
							parseTaskInput(&last.blocks[i], last.blocks[i].ToolInput)
						}
					}
				}
			}
			m.updateSessionStats(msg.Response)
		}
		m.refreshViewport()
		return m, nil

	case ClaudeResponseMsg:
		if msg.Err != nil {
			m.entries = append(m.entries, chatEntry{role: "error", text: msg.Err.Error()})
		} else {
			if msg.Response.Result.SessionID != "" {
				m.sessionID = msg.Response.Result.SessionID
			}
			blocks := msg.Response.ExtractBlocks()

			m.entries = append(m.entries, chatEntry{
				role:          "assistant",
				text:          msg.Response.AssistantText(),
				blocks:        blocks,
				result:        msg.Response.Result,
				model:         msg.Response.Model,
				stopReason:    msg.Response.StopReason,
				hasResult:     true,
				cacheReadTok:  msg.Response.Result.Usage.CacheReadInputTokens,
				durationMs:    msg.Response.Result.DurationMs,
				durationAPIMs: msg.Response.Result.DurationAPIMs,
			})

			m.updateSessionStats(msg.Response)
		}
		m.refreshViewport()
		return m, nil
	}

	// Route mouse wheel to viewport, everything else to textarea only
	var cmd tea.Cmd
	switch msg.(type) {
	case tea.MouseWheelMsg:
		m.viewport, cmd = m.viewport.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	default:
		m.textarea, cmd = m.textarea.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}

	return m, tea.Batch(cmds...)
}

func (m *ChatModel) refreshViewport() {
	var sb strings.Builder

	// Styles
	userLabelStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("15")).
		Background(lipgloss.Color("4")).
		Padding(0, 1)
	claudeLabelStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("15")).
		Background(lipgloss.Color("208")).
		Padding(0, 1)
	errLabelStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("15")).
		Background(lipgloss.Color("1")).
		Padding(0, 1)
	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8"))
	toolNameStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("3"))
	toolBorderStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8"))
	toolInputStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("6"))
	toolOutputStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("7"))
	toolErrStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("9"))
	thinkingStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8")).
		Italic(true)
	thinkingLabelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8")).
		Bold(true)

	contentWidth := m.width - 4
	if contentWidth < 20 {
		contentWidth = 20
	}

	separator := dimStyle.Render(strings.Repeat("─", m.width))

	// Init banner
	if m.initReceived {
		bannerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
		var info []string
		if m.initModel != "" {
			info = append(info, m.initModel)
		}
		if m.initVersion != "" {
			info = append(info, "v"+m.initVersion)
		}
		if m.initNumTools > 0 {
			info = append(info, fmt.Sprintf("%d tools", m.initNumTools))
		}
		if m.initPermMode != "" {
			info = append(info, m.initPermMode)
		}
		for _, p := range m.initPlugins {
			info = append(info, p)
		}
		sb.WriteString(bannerStyle.Render("  "+strings.Join(info, " · ")))
		sb.WriteString("\n\n")
	}

	for i, e := range m.entries {
		if i > 0 {
			sb.WriteString("\n" + separator + "\n\n")
		}

		switch e.role {
		case "user":
			sb.WriteString(userLabelStyle.Render("YOU"))
			sb.WriteString("\n\n")
			sb.WriteString(e.text)
			sb.WriteString("\n")

		case "assistant":
			sb.WriteString(claudeLabelStyle.Render("CLAUDE"))
			sb.WriteString("\n")

			// Render thinking if present
			if e.thinking != "" {
				sb.WriteString("\n")
				sb.WriteString(thinkingLabelStyle.Render("  Thinking"))
				sb.WriteString("\n")
				sb.WriteString(thinkingStyle.Render("  " + strings.ReplaceAll(e.thinking, "\n", "\n  ")))
				sb.WriteString("\n")
			}

			if len(e.blocks) > 0 {
				m.renderBlocks(&sb, e.blocks, toolNameStyle, toolBorderStyle,
					toolInputStyle, toolOutputStyle, toolErrStyle, dimStyle, contentWidth, false)
			} else if e.text != "" {
				sb.WriteString("\n")
				if m.renderer != nil {
					rendered, err := m.renderer.Render(e.text)
					if err == nil {
						sb.WriteString(strings.TrimSpace(rendered))
					} else {
						sb.WriteString(e.text)
					}
				} else {
					sb.WriteString(e.text)
				}
				sb.WriteString("\n")
			}

			// Per-message metadata line
			if e.hasResult {
				sb.WriteString(renderMessageMeta(e, dimStyle))
			}

		case "error":
			sb.WriteString(errLabelStyle.Render("ERROR"))
			sb.WriteString("\n\n")
			sb.WriteString(e.text)
			sb.WriteString("\n")
		}
	}

	m.cachedContent = sb.String()
	wasAtBottom := m.viewport.AtBottom()
	m.viewport.SetContent(m.cachedContent)
	if wasAtBottom {
		m.viewport.GotoBottom()
	}
}

// refreshStreamingViewport efficiently updates the viewport during streaming.
// It reuses the cached content for finalized entries and renders the streaming
// entry's blocks without glamour rendering.
func (m *ChatModel) refreshStreamingViewport() {
	claudeLabelStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("15")).
		Background(lipgloss.Color("208")).
		Padding(0, 1)
	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8"))
	toolNameStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("3"))
	toolBorderStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8"))
	toolInputStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("6"))
	toolOutputStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("7"))
	toolErrStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("9"))

	contentWidth := m.width - 4
	if contentWidth < 20 {
		contentWidth = 20
	}

	separator := dimStyle.Render(strings.Repeat("─", m.width))

	var sb strings.Builder

	// Init banner if no cached content yet
	if m.cachedContent == "" && m.initReceived {
		bannerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
		var info []string
		if m.initModel != "" {
			info = append(info, m.initModel)
		}
		if m.initVersion != "" {
			info = append(info, "v"+m.initVersion)
		}
		if m.initNumTools > 0 {
			info = append(info, fmt.Sprintf("%d tools", m.initNumTools))
		}
		if m.initPermMode != "" {
			info = append(info, m.initPermMode)
		}
		for _, p := range m.initPlugins {
			info = append(info, p)
		}
		sb.WriteString(bannerStyle.Render("  " + strings.Join(info, " · ")))
		sb.WriteString("\n\n")
	} else {
		// Use cached content for all finalized entries
		sb.WriteString(m.cachedContent)
	}

	// Append the streaming entry
	if len(m.entries) > 0 {
		last := &m.entries[len(m.entries)-1]
		if last.streaming {
			if sb.Len() > 0 {
				sb.WriteString("\n" + separator + "\n\n")
			}
			sb.WriteString(claudeLabelStyle.Render("CLAUDE"))
			sb.WriteString("\n")

			thinkingStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("8")).
				Italic(true)
			thinkingLabelStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("8")).
				Bold(true)

			if last.streamThinking != "" {
				sb.WriteString("\n")
				sb.WriteString(thinkingLabelStyle.Render("  Thinking..."))
				sb.WriteString("\n")
				sb.WriteString(thinkingStyle.Render("  " + strings.ReplaceAll(last.streamThinking, "\n", "\n  ")))
				sb.WriteString("\n")
			}

			// Render blocks incrementally (raw=true skips glamour)
			if len(last.blocks) > 0 {
				m.renderBlocks(&sb, last.blocks, toolNameStyle, toolBorderStyle,
					toolInputStyle, toolOutputStyle, toolErrStyle, dimStyle, contentWidth, true)
			}
			sb.WriteString("\n")
		}
	}

	wasAtBottom := m.viewport.AtBottom()
	m.viewport.SetContent(sb.String())
	if wasAtBottom {
		m.viewport.GotoBottom()
	}
}

func (m *ChatModel) renderBlocks(sb *strings.Builder, blocks []ChatBlock,
	toolNameStyle, toolBorderStyle, toolInputStyle, toolOutputStyle, toolErrStyle, dimStyle lipgloss.Style,
	contentWidth int, raw bool,
) {
	// Group tool_use and tool_result by ToolID for inline rendering
	resultMap := make(map[string]*ChatBlock)
	for i := range blocks {
		if blocks[i].Kind == "tool_result" {
			resultMap[blocks[i].ToolID] = &blocks[i]
		}
	}

	for _, block := range blocks {
		switch block.Kind {
		case "text":
			if block.Text == "" {
				continue
			}
			sb.WriteString("\n")
			if !raw && m.renderer != nil {
				rendered, err := m.renderer.Render(block.Text)
				if err == nil {
					sb.WriteString(strings.TrimSpace(rendered))
				} else {
					sb.WriteString(block.Text)
				}
			} else {
				sb.WriteString(block.Text)
			}
			sb.WriteString("\n")

		case "tool_use":
			sb.WriteString("\n")

			if block.IsTask {
				m.renderTaskBlock(sb, block, resultMap[block.ToolID],
					toolNameStyle, toolBorderStyle, toolInputStyle, toolOutputStyle, toolErrStyle, dimStyle,
					contentWidth)
				continue
			}

			// Compact rendering for Read tool calls
			if block.ToolName == "Read" {
				m.renderCompactTool(sb, block, resultMap[block.ToolID],
					toolNameStyle, toolInputStyle, toolErrStyle, dimStyle, contentWidth)
				continue
			}

			boxWidth := contentWidth - 2
			if boxWidth < 20 {
				boxWidth = 20
			}

			header := fmt.Sprintf("┌─ %s %s", toolNameStyle.Render("⚙ "+block.ToolName),
				toolBorderStyle.Render(strings.Repeat("─", max(0, boxWidth-len(block.ToolName)-6))))
			sb.WriteString(toolBorderStyle.Render("  ") + header + "\n")

			// Tool input
			inputLines := strings.Split(block.ToolInput, "\n")
			for _, line := range inputLines {
				if len(line) > boxWidth-4 {
					line = line[:boxWidth-7] + "..."
				}
				sb.WriteString(toolBorderStyle.Render("  │ ") + toolInputStyle.Render(line) + "\n")
			}

			// Tool result (if matched)
			if result, ok := resultMap[block.ToolID]; ok {
				sb.WriteString(toolBorderStyle.Render("  ├─ "))
				if result.IsError {
					sb.WriteString(toolErrStyle.Render("✗ Error") + "\n")
				} else {
					sb.WriteString(dimStyle.Render("✓ Result") + "\n")
				}

				output := result.ToolOutput
				outputLines := strings.Split(output, "\n")
				maxLines := 15
				truncated := false
				if len(outputLines) > maxLines {
					outputLines = outputLines[:maxLines]
					truncated = true
				}
				for _, line := range outputLines {
					if len(line) > boxWidth-4 {
						line = line[:boxWidth-7] + "..."
					}
					style := toolOutputStyle
					if result.IsError {
						style = toolErrStyle
					}
					sb.WriteString(toolBorderStyle.Render("  │ ") + style.Render(line) + "\n")
				}
				if truncated {
					sb.WriteString(toolBorderStyle.Render("  │ ") + dimStyle.Render(fmt.Sprintf("... (%d more lines)", len(strings.Split(output, "\n"))-maxLines)) + "\n")
				}
			}

			// Close box
			footer := fmt.Sprintf("└%s", strings.Repeat("─", max(0, boxWidth)))
			sb.WriteString(toolBorderStyle.Render("  "+footer) + "\n")

		case "tool_result":
			continue
		}
	}
}


// renderMessageMeta formats the per-message metadata line.
func renderMessageMeta(e chatEntry, dimStyle lipgloss.Style) string {
	var parts []string

	if e.model != "" {
		parts = append(parts, e.model)
	}

	parts = append(parts, fmt.Sprintf("$%.4f", e.result.CostUSD))

	parts = append(parts, fmt.Sprintf("%s out / %s in",
		formatTokens(e.result.Usage.OutputTokens),
		formatTokens(e.result.Usage.InputTokens)))

	// Cache hit ratio
	totalIn := e.result.Usage.InputTokens + e.cacheReadTok
	if totalIn > 0 && e.cacheReadTok > 0 {
		pct := float64(e.cacheReadTok) / float64(totalIn) * 100
		parts = append(parts, fmt.Sprintf("%d%% cache", int(pct)))
	}

	// Duration
	if e.durationMs > 0 {
		if e.durationAPIMs > 0 {
			parts = append(parts, fmt.Sprintf("API %.1fs / %.1fs total",
				float64(e.durationAPIMs)/1000, float64(e.durationMs)/1000))
		} else {
			parts = append(parts, fmt.Sprintf("%.1fs", float64(e.durationMs)/1000))
		}
	}

	return dimStyle.Render("  "+strings.Join(parts, " · ")) + "\n"
}

// lastBlockIndex returns the index of the last block with the given kind, or -1.
func lastBlockIndex(blocks []ChatBlock, kind string) int {
	for i := len(blocks) - 1; i >= 0; i-- {
		if blocks[i].Kind == kind {
			return i
		}
	}
	return -1
}

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
							var pretty bytes.Buffer
							if json.Indent(&pretty, block.Input, "", "  ") == nil {
								inputStr = pretty.String()
							} else {
								inputStr = string(block.Input)
							}
						}
						entry.blocks[taskIdx].TaskSubBlocks = append(entry.blocks[taskIdx].TaskSubBlocks, ChatBlock{
							Kind:      "tool_use",
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
						output := extractToolResultContent(block.Content)
						entry.blocks[taskIdx].TaskSubBlocks = append(entry.blocks[taskIdx].TaskSubBlocks, ChatBlock{
							Kind:       "tool_result",
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
				entry.blocks = append(entry.blocks, ChatBlock{Kind: "text"})
			case "tool_use":
				cb := ChatBlock{
					Kind:     "tool_use",
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
						output = extractTaskResultContent(block.Content)
						entry.blocks[taskIdx].TaskMeta = parseToolUseResult(ev.Raw)
					} else {
						output = extractToolResultContent(block.Content)
					}
					entry.blocks = append(entry.blocks, ChatBlock{
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

// addContentBlock parses a top-level content_block_start event and adds the block.
func (m *ChatModel) addContentBlock(entry *chatEntry, raw string) {
	var cbs struct {
		ContentBlock ContentBlock `json:"content_block"`
	}
	if json.Unmarshal([]byte(raw), &cbs) == nil {
		switch cbs.ContentBlock.Type {
		case "text":
			entry.blocks = append(entry.blocks, ChatBlock{Kind: "text"})
		case "tool_use":
			cb := ChatBlock{
				Kind:     "tool_use",
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

// extractToolResultContent converts tool result content (string, array, or other) to a string.
func extractToolResultContent(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var out string
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if t, ok := m["text"].(string); ok {
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

func formatTokens(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

// parseInitEvent extracts startup metadata from the system/init event.
func (m *ChatModel) parseInitEvent(ev StreamEvent) {
	var init struct {
		Model     string   `json:"model"`
		Version   string   `json:"claude_code_version"`
		Tools     []string `json:"tools"`
		PermMode  string   `json:"permissionMode"`
		Plugins   []struct {
			Name string `json:"name"`
		} `json:"plugins"`
	}
	if json.Unmarshal([]byte(ev.Raw), &init) == nil {
		m.initModel = init.Model
		m.initVersion = init.Version
		m.initNumTools = len(init.Tools)
		m.initPermMode = init.PermMode
		for _, p := range init.Plugins {
			m.initPlugins = append(m.initPlugins, p.Name)
		}
		m.initReceived = true
	}
}

// parseRateLimitEvent extracts rate limit info from a rate_limit_event.
func (m *ChatModel) parseRateLimitEvent(ev StreamEvent) {
	var rl struct {
		RateLimitInfo struct {
			Status       string `json:"status"`
			ResetsAt     int64  `json:"resetsAt"`
			OverageStatus string `json:"overageStatus"`
			IsUsingOverage bool  `json:"isUsingOverage"`
		} `json:"rate_limit_info"`
	}
	if json.Unmarshal([]byte(ev.Raw), &rl) == nil {
		m.rateLimitStatus = rl.RateLimitInfo.Status
		m.rateLimitResetsAt = time.Unix(rl.RateLimitInfo.ResetsAt, 0)
		m.rateLimitOverage = rl.RateLimitInfo.OverageStatus
		m.rateLimitIsOverage = rl.RateLimitInfo.IsUsingOverage
	}
}

// updateSessionStats updates cumulative and per-request stats from a response.
func (m *ChatModel) updateSessionStats(resp *ClaudeResponse) {
	m.totalRequests++
	m.totalCost += resp.Result.CostUSD
	m.totalInputTok += resp.Result.Usage.InputTokens
	m.totalOutputTok += resp.Result.Usage.OutputTokens
	m.lastModel = resp.Model
	m.lastCost = resp.Result.CostUSD
	m.lastInputTok = resp.Result.Usage.InputTokens
	m.lastOutputTok = resp.Result.Usage.OutputTokens
	m.lastCacheRead = resp.Result.Usage.CacheReadInputTokens
	m.lastDurationMs = resp.Result.DurationMs
	m.lastAPIMs = resp.Result.DurationAPIMs
}

// SetSize updates the chat tab dimensions.
func (m *ChatModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	textareaHeight := 3
	// 1 for divider, 1 for status line
	viewportHeight := h - textareaHeight - 2

	m.viewport.SetWidth(w)
	m.viewport.SetHeight(viewportHeight)
	m.textarea.SetWidth(w)
	m.textarea.SetHeight(textareaHeight)

	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(styles.DarkStyle),
		glamour.WithWordWrap(w-4),
	)
	if err == nil {
		m.renderer = r
	}
	m.refreshViewport()
}

// renderStatusLine builds the persistent status line below the textarea.
func (m ChatModel) renderStatusLine() string {
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("238"))

	if m.totalRequests == 0 && m.initReceived {
		info := m.initModel + " ready"
		if p := WireLogPath(); p != "" {
			info += " · wire: " + p
		}
		return dimStyle.Render(info)
	} else if m.totalRequests == 0 {
		return dimStyle.Render("ready")
	}

	sep := sepStyle.Render(" │ ")
	var parts []string

	// Model name (short form)
	model := m.lastModel
	parts = append(parts, dimStyle.Render(model))

	// Last request: cost + latency
	last := fmt.Sprintf("$%.4f", m.lastCost)
	if m.lastDurationMs > 0 {
		if m.lastAPIMs > 0 {
			last += fmt.Sprintf(" %.1fs/%.1fs", float64(m.lastAPIMs)/1000, float64(m.lastDurationMs)/1000)
		} else {
			last += fmt.Sprintf(" %.1fs", float64(m.lastDurationMs)/1000)
		}
	}
	// Cache hit ratio
	totalIn := m.lastInputTok + m.lastCacheRead
	if totalIn > 0 && m.lastCacheRead > 0 {
		pct := float64(m.lastCacheRead) / float64(totalIn) * 100
		last += fmt.Sprintf(" %d%%cache", int(pct))
	}
	parts = append(parts, dimStyle.Render(last))

	// Session totals
	session := fmt.Sprintf("$%.4f %dreqs", m.totalCost, m.totalRequests)
	parts = append(parts, dimStyle.Render(session))

	// Rate limit status
	if m.rateLimitStatus != "" {
		rlStyle := dimStyle
		rl := m.rateLimitStatus
		if m.rateLimitIsOverage {
			rlStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
			rl = "overage"
		} else if m.rateLimitStatus == "throttled" {
			rlStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
		}
		parts = append(parts, rlStyle.Render(rl))
	}

	return strings.Join(parts, sep)
}

// renderCompactTool renders a tool call in the compact subagent-activity style:
//
//	⚙ Read
//	  /path/to/file
//	  ✓ package main
func (m *ChatModel) renderCompactTool(sb *strings.Builder, block ChatBlock, result *ChatBlock,
	toolNameStyle, toolInputStyle, toolErrStyle, dimStyle lipgloss.Style,
	contentWidth int,
) {
	maxLen := contentWidth - 6
	if maxLen < 20 {
		maxLen = 20
	}

	sb.WriteString("  " + toolNameStyle.Render("⚙ "+block.ToolName) + "\n")

	// Input summary
	inputLine := toolInputSummary(block.ToolName, block.ToolInput, maxLen)
	if inputLine != "" {
		sb.WriteString("    " + toolInputStyle.Render(inputLine) + "\n")
	}

	// Result summary
	if result != nil {
		if result.IsError {
			cleaned := cleanToolOutput(result.ToolOutput)
			outputLine := firstLine(cleaned, maxLen-4)
			sb.WriteString("    " + toolErrStyle.Render("✗ "+outputLine) + "\n")
		} else {
			cleaned := cleanToolOutput(result.ToolOutput)
			outputLine := firstLine(cleaned, maxLen-4)
			sb.WriteString("    " + dimStyle.Render("✓ "+outputLine) + "\n")
		}
	}
}

// renderTaskBlock renders a Task (subagent) tool call with header, subagent activity, result, and metadata footer.
func (m *ChatModel) renderTaskBlock(sb *strings.Builder, block ChatBlock, result *ChatBlock,
	toolNameStyle, toolBorderStyle, toolInputStyle, toolOutputStyle, toolErrStyle, dimStyle lipgloss.Style,
	contentWidth int,
) {
	boxWidth := contentWidth - 2
	if boxWidth < 20 {
		boxWidth = 20
	}

	// Header with subagent type
	label := block.TaskSubagentType
	if label == "" {
		label = "Task"
	}
	header := fmt.Sprintf("┌─ %s %s", toolNameStyle.Render("⚙ "+label),
		toolBorderStyle.Render(strings.Repeat("─", max(0, boxWidth-len(label)-6))))
	sb.WriteString(toolBorderStyle.Render("  ") + header + "\n")

	// Description field
	renderTaskField(sb, "desc", block.TaskDescription, boxWidth, toolBorderStyle, toolInputStyle)

	// Prompt field (with wrapping)
	if block.TaskPrompt != "" {
		renderTaskField(sb, "prompt", block.TaskPrompt, boxWidth, toolBorderStyle, toolInputStyle)
	}

	// Subagent activity
	if len(block.TaskSubBlocks) > 0 {
		sb.WriteString(toolBorderStyle.Render("  ├─ ") + dimStyle.Render("Subagent Activity") + "\n")

		// Build result map for sub-blocks
		subResultMap := make(map[string]*ChatBlock)
		for i := range block.TaskSubBlocks {
			if block.TaskSubBlocks[i].Kind == "tool_result" {
				subResultMap[block.TaskSubBlocks[i].ToolID] = &block.TaskSubBlocks[i]
			}
		}

		for _, sub := range block.TaskSubBlocks {
			if sub.Kind == "tool_use" {
				sb.WriteString(toolBorderStyle.Render("  │  ") + toolNameStyle.Render("⚙ "+sub.ToolName) + "\n")

				// Show meaningful summary of input
				inputLine := toolInputSummary(sub.ToolName, sub.ToolInput, boxWidth-8)
				if inputLine != "" {
					sb.WriteString(toolBorderStyle.Render("  │    ") + toolInputStyle.Render(inputLine) + "\n")
				}

				// Show first line of result
				if res, ok := subResultMap[sub.ToolID]; ok {
					marker := "✓"
					style := dimStyle
					if res.IsError {
						marker = "✗"
						style = toolErrStyle
					}
					cleaned := cleanToolOutput(res.ToolOutput)
					outputLine := firstLine(cleaned, boxWidth-10)
					sb.WriteString(toolBorderStyle.Render("  │    ") + style.Render(marker+" "+outputLine) + "\n")
				}
			}
		}
	}

	// Result (from the tool_result block)
	if result != nil {
		sb.WriteString(toolBorderStyle.Render("  ├─ "))
		if result.IsError {
			sb.WriteString(toolErrStyle.Render("✗ Error") + "\n")
		} else {
			sb.WriteString(dimStyle.Render("✓ Result") + "\n")
		}

		output := result.ToolOutput
		outputLines := strings.Split(output, "\n")
		maxLines := 15
		truncated := false
		if len(outputLines) > maxLines {
			outputLines = outputLines[:maxLines]
			truncated = true
		}
		for _, line := range outputLines {
			if len(line) > boxWidth-4 {
				line = line[:boxWidth-7] + "..."
			}
			style := toolOutputStyle
			if result.IsError {
				style = toolErrStyle
			}
			sb.WriteString(toolBorderStyle.Render("  │ ") + style.Render(line) + "\n")
		}
		if truncated {
			sb.WriteString(toolBorderStyle.Render("  │ ") + dimStyle.Render(fmt.Sprintf("... (%d more lines)", len(strings.Split(output, "\n"))-maxLines)) + "\n")
		}
	}

	// Metadata footer
	if block.TaskMeta != nil {
		meta := block.TaskMeta
		var metaParts []string
		if meta.AgentID != "" {
			id := meta.AgentID
			if len(id) > 8 {
				id = id[:8]
			}
			metaParts = append(metaParts, id)
		}
		if meta.TotalDurationMs > 0 {
			metaParts = append(metaParts, fmt.Sprintf("%.1fs", float64(meta.TotalDurationMs)/1000))
		}
		if meta.TotalTokens > 0 {
			metaParts = append(metaParts, formatTokens(meta.TotalTokens)+" tok")
		}
		if meta.TotalToolUseCount > 0 {
			metaParts = append(metaParts, fmt.Sprintf("%d tools", meta.TotalToolUseCount))
		}
		if len(metaParts) > 0 {
			sb.WriteString(toolBorderStyle.Render("  ├─ ") + dimStyle.Render(strings.Join(metaParts, " · ")) + "\n")
		}
	}

	// Close box
	footer := fmt.Sprintf("└%s", strings.Repeat("─", max(0, boxWidth)))
	sb.WriteString(toolBorderStyle.Render("  "+footer) + "\n")
}

// renderTaskField renders a labeled field with text wrapping inside a tool box.
// Long values are truncated after maxFieldLines lines.
func renderTaskField(sb *strings.Builder, label, value string, boxWidth int,
	borderStyle, valueStyle lipgloss.Style) {
	if value == "" {
		return
	}
	prefix := label + ":"
	padLen := 9 - len(prefix)
	if padLen < 1 {
		padLen = 1
	}
	prefix += strings.Repeat(" ", padLen)

	// Wrap text within box width
	wrapWidth := boxWidth - len(prefix) - 4
	if wrapWidth < 20 {
		wrapWidth = 20
	}
	lines := wrapText(value, wrapWidth)

	const maxFieldLines = 6
	truncated := false
	if len(lines) > maxFieldLines {
		lines = lines[:maxFieldLines]
		truncated = true
	}

	for i, line := range lines {
		if i == 0 {
			sb.WriteString(borderStyle.Render("  │ ") + valueStyle.Render(prefix+line) + "\n")
		} else {
			sb.WriteString(borderStyle.Render("  │ ") + valueStyle.Render(strings.Repeat(" ", len(prefix))+line) + "\n")
		}
	}
	if truncated {
		dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
		sb.WriteString(borderStyle.Render("  │ ") + dimStyle.Render(strings.Repeat(" ", len(prefix))+"...") + "\n")
	}
}

// wrapText breaks text into lines of at most width characters, preserving
// existing newlines and preferring word boundaries.
func wrapText(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	// Split on existing newlines first
	paragraphs := strings.Split(text, "\n")
	var lines []string
	for _, para := range paragraphs {
		if len(para) <= width {
			lines = append(lines, para)
			continue
		}
		// Wrap this paragraph
		remaining := para
		for len(remaining) > width {
			breakAt := strings.LastIndex(remaining[:width], " ")
			if breakAt <= 0 {
				breakAt = width
			}
			lines = append(lines, remaining[:breakAt])
			remaining = strings.TrimLeft(remaining[breakAt:], " ")
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
	if maxLen > 0 && len(s) > maxLen {
		s = s[:maxLen-3] + "..."
	}
	return s
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

// View renders the chat tab.
func (m ChatModel) View() string {
	var divider string
	if m.scrollMode {
		scrollStyle := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("3"))
		pct := int(m.viewport.ScrollPercent() * 100)
		label := fmt.Sprintf(" SCROLL (esc to exit) %d%% ", pct)
		pad := m.width - lipgloss.Width(label)
		if pad < 0 {
			pad = 0
		}
		divider = scrollStyle.Render(label) + lipgloss.NewStyle().
			Foreground(lipgloss.Color("3")).
			Render(strings.Repeat("─", pad))
	} else {
		divider = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")).
			Render(strings.Repeat("─", m.width))
	}

	return fmt.Sprintf("%s\n%s\n%s\n%s",
		m.viewport.View(),
		m.renderStatusLine(),
		divider,
		m.textarea.View(),
	)
}
