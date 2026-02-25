package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
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

type cardZone struct {
	id        string
	startLine int
	endLine   int
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
	permMode      PermissionMode  // current permission mode for claude CLI

	// Session-level cumulative stats for status line
	totalCost      float64
	totalInputTok  int
	totalOutputTok int
	totalRequests  int
	lastModel      string
	lastCost       float64
	lastInputTok   int
	lastCacheRead     int
	lastCacheCreation int
	lastDurationMs    int
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

	// Active streaming process, for cancellation on ctrl+c
	streamCmd *exec.Cmd

	// Interactive mode (experimental goexpect-based session)
	interactive   bool                 // use interactive session instead of print mode
	iSession      *InteractiveSession  // persistent interactive session
	iStreamCh     <-chan string         // current interactive response stream

	// Cached lipgloss styles (initialized in NewChatModel, updated in SetSize)
	styleUserCard      lipgloss.Style
	styleErrorCard     lipgloss.Style
	styleToolResultCard lipgloss.Style
	styleDim           lipgloss.Style
	styleToolName      lipgloss.Style
	styleToolInput     lipgloss.Style
	styleToolOutput    lipgloss.Style
	styleToolErr       lipgloss.Style
	styleUserLabel     lipgloss.Style
	styleThinkingCard  lipgloss.Style
	styleThinkingLabel lipgloss.Style
	styleHeaderCard    lipgloss.Style
	styleAssistantCard lipgloss.Style
	styleToolCard      lipgloss.Style

	// Layout padding
	padH int // horizontal padding (each side)

	// Collapsible card state
	expandedCards      map[string]bool // card ID → expanded
	cardZones          []cardZone      // rebuilt each render
	cachedCardZoneCount int            // zones from finalized entries
	cachedLineCount    int             // line count of cached content
}

// NewChatModel creates a new chat tab model.
func NewChatModel() *ChatModel {
	ta := textarea.New()
	ta.Placeholder = "Type a message..."
	ta.SetHeight(3)
	ta.CharLimit = 0

	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	vp.SoftWrap = true
	vp.KeyMap.Left = key.NewBinding(key.WithDisabled())
	vp.KeyMap.Right = key.NewBinding(key.WithDisabled())

	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(styles.DarkStyle),
		glamour.WithWordWrap(73),
	)
	if err != nil {
		log.Printf("glamour renderer init failed: %v (markdown rendering disabled)", err)
	}

	return &ChatModel{
		viewport: vp,
		textarea: ta,
		renderer: r,
		permMode: PermAcceptEdits,
		styleUserCard: lipgloss.NewStyle().
			BorderLeft(true).
			BorderStyle(lipgloss.ThickBorder()).
			BorderForeground(lipgloss.Color("4")).
			Background(lipgloss.Color("236")).
			PaddingLeft(1).
			PaddingRight(1),
		styleErrorCard: lipgloss.NewStyle().
			BorderLeft(true).
			BorderStyle(lipgloss.ThickBorder()).
			BorderForeground(lipgloss.Color("1")).
			Background(lipgloss.Color("236")).
			PaddingLeft(1).
			PaddingRight(1),
		styleToolResultCard: lipgloss.NewStyle().
			Background(lipgloss.Color("235")).
			PaddingLeft(1).
			PaddingRight(1),
		styleDim: lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")),
		styleToolName: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("3")),
		styleToolInput: lipgloss.NewStyle().
			Foreground(lipgloss.Color("6")),
		styleToolOutput: lipgloss.NewStyle().
			Foreground(lipgloss.Color("7")),
		styleToolErr: lipgloss.NewStyle().
			Foreground(lipgloss.Color("9")),
		styleUserLabel: lipgloss.NewStyle().
			Foreground(lipgloss.Color("4")).
			Background(lipgloss.Color("236")).
			Bold(true),
		styleThinkingCard: lipgloss.NewStyle().
			BorderLeft(true).
			BorderStyle(lipgloss.ThickBorder()).
			BorderForeground(lipgloss.Color("243")).
			Background(lipgloss.Color("236")).
			PaddingLeft(1).
			PaddingRight(1).
			Foreground(lipgloss.Color("243")).
			Italic(true),
		styleThinkingLabel: lipgloss.NewStyle().
			Foreground(lipgloss.Color("243")).
			Background(lipgloss.Color("236")).
			Bold(true).
			Italic(true),
		styleHeaderCard: lipgloss.NewStyle().
			Background(lipgloss.Color("236")).
			PaddingLeft(1).
			PaddingRight(1),
		styleAssistantCard: lipgloss.NewStyle().
			BorderLeft(true).
			BorderStyle(lipgloss.ThickBorder()).
			BorderForeground(lipgloss.Color("208")).
			PaddingLeft(1),
		styleToolCard: lipgloss.NewStyle().
			BorderLeft(true).
			BorderStyle(lipgloss.ThickBorder()).
			BorderForeground(lipgloss.Color("3")).
			PaddingLeft(1),
		padH:          2,
		expandedCards: make(map[string]bool),
	}
}

// Init returns the initial command (focus textarea).
func (m *ChatModel) Init() tea.Cmd {
	return m.textarea.Focus()
}

// Update handles messages for the chat tab.
func (m *ChatModel) Update(msg tea.Msg) tea.Cmd {
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
			return tea.Batch(cmds...)
		}
		if m.scrollMode {
			// Exit scroll mode on i or enter
			if msg.String() == "i" || msg.String() == "enter" {
				m.scrollMode = false
				return m.textarea.Focus()
			}
			// Route all other keys to viewport
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return cmd
		}

		if msg.String() == "ctrl+p" {
			m.permMode = m.permMode.Next()
			return nil
		}
		if msg.String() == "shift+enter" {
			m.textarea.InsertRune('\n')
			return nil
		}
		if msg.String() == "enter" {
			text := strings.TrimSpace(m.textarea.Value())
			if text != "" && m.streamCh == nil && m.iStreamCh == nil {
				m.textarea.Reset()
				m.entries = append(m.entries, chatEntry{role: "user", text: text})
				m.refreshViewport()

				if m.interactive {
					// Interactive mode: use goexpect session
					session := m.iSession
					cmds = append(cmds, func() tea.Msg {
						if session == nil {
							s, err := StartInteractive()
							if err != nil {
								return InteractiveDoneMsg{Err: err}
							}
							return InteractiveStartMsg{Session: s}
						}
						ch, err := session.SendPrompt(text)
						if err != nil {
							return InteractiveDoneMsg{Err: err}
						}
						return interactiveStreamStartMsg{ch: ch}
					})
				} else {
					// Print mode: spawn new process per message
					sid := m.sessionID
					pm := m.permMode
					cmds = append(cmds, func() tea.Msg {
						ch, cmd, err := StreamClaude(text, sid, pm)
						if err != nil {
							return ClaudeStreamDoneMsg{Prompt: text, Err: err}
						}
						return ClaudeStreamStartMsg{Prompt: text, Ch: ch, Cmd: cmd}
					})
				}
			}
			return tea.Batch(cmds...)
		}

	case ClaudeStreamStartMsg:
		m.streamCh = msg.Ch
		m.streamCmd = msg.Cmd
		m.entries = append(m.entries, chatEntry{
			role:      "assistant",
			streaming: true,
		})
		m.refreshStreamingViewport()
		return waitForStreamMsg(msg.Ch)

	case ClaudeStreamChunkMsg:
		if m.streamCh == nil {
			return nil
		}
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
					// Append to last thinking block, auto-create if none
					if idx := lastBlockIndex(last.blocks, BlockThinking); idx >= 0 {
						last.blocks[idx].Text += msg.ThinkingDelta
					} else {
						last.blocks = append(last.blocks, ChatBlock{Kind: BlockThinking, Text: msg.ThinkingDelta})
					}
				}
				if msg.TextDelta != "" {
					last.streamText += msg.TextDelta
					// Append to last text block, auto-create if none
					if idx := lastBlockIndex(last.blocks, BlockText); idx >= 0 {
						last.blocks[idx].Text += msg.TextDelta
					} else {
						last.blocks = append(last.blocks, ChatBlock{Kind: BlockText, Text: msg.TextDelta})
					}
				}
				if msg.InputJSONDelta != "" {
					if idx := lastBlockIndex(last.blocks, BlockToolUse); idx >= 0 {
						last.blocks[idx].ToolInput += msg.InputJSONDelta
					}
				}
				m.refreshStreamingViewport()
			}
		}
		if m.streamCh != nil {
			return waitForStreamMsg(m.streamCh)
		}
		return nil

	case ClaudeStreamDoneMsg:
		m.streamCh = nil
		m.streamCmd = nil
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
				last.result = msg.Response.Result
				last.model = msg.Response.Model
				last.stopReason = msg.Response.StopReason
				last.hasResult = true
				last.cacheReadTok = msg.Response.Result.Usage.CacheReadInputTokens
				last.durationMs = msg.Response.Result.DurationMs
				last.durationAPIMs = msg.Response.Result.DurationAPIMs

				// Pretty-print tool input JSON and parse Task inputs now that streaming is done
				for i := range last.blocks {
					if last.blocks[i].Kind == BlockToolUse && last.blocks[i].ToolInput != "" {
						last.blocks[i].ToolInput = prettyJSON([]byte(last.blocks[i].ToolInput))
						if last.blocks[i].IsTask {
							parseTaskInput(&last.blocks[i], last.blocks[i].ToolInput)
						}
					}
				}
			}
			m.updateSessionStats(msg.Response)
		}
		m.refreshViewport()
		return nil

	case InteractiveStartMsg:
		m.iSession = msg.Session
		// Re-send the last user prompt now that session is ready
		if len(m.entries) > 0 {
			lastEntry := m.entries[len(m.entries)-1]
			if lastEntry.role == "user" {
				session := m.iSession
				text := lastEntry.text
				return func() tea.Msg {
					ch, err := session.SendPrompt(text)
					if err != nil {
						return InteractiveDoneMsg{Err: err}
					}
					return interactiveStreamStartMsg{ch: ch}
				}
			}
		}
		return nil

	case interactiveStreamStartMsg:
		m.iStreamCh = msg.ch
		m.entries = append(m.entries, chatEntry{
			role:      "assistant",
			streaming: true,
		})
		m.refreshStreamingViewport()
		return waitForInteractiveMsg(msg.ch)

	case InteractiveChunkMsg:
		if m.iStreamCh == nil {
			return nil
		}
		if len(m.entries) > 0 {
			last := &m.entries[len(m.entries)-1]
			if last.streaming {
				last.streamText += msg.Text
				// Update or create a text block
				if idx := lastBlockIndex(last.blocks, BlockText); idx >= 0 {
					last.blocks[idx].Text += msg.Text
				} else {
					last.blocks = append(last.blocks, ChatBlock{Kind: BlockText, Text: msg.Text})
				}
				m.refreshStreamingViewport()
			}
		}
		if m.iStreamCh != nil {
			return waitForInteractiveMsg(m.iStreamCh)
		}
		return nil

	case InteractiveDoneMsg:
		m.iStreamCh = nil
		if msg.Err != nil {
			if len(m.entries) > 0 && m.entries[len(m.entries)-1].streaming {
				m.entries[len(m.entries)-1] = chatEntry{role: "error", text: msg.Err.Error()}
			} else {
				m.entries = append(m.entries, chatEntry{role: "error", text: msg.Err.Error()})
			}
		} else if len(m.entries) > 0 && m.entries[len(m.entries)-1].streaming {
			last := &m.entries[len(m.entries)-1]
			last.streaming = false
			last.text = last.streamText
		}
		m.refreshViewport()
		return nil

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
		return nil
	}

	// Route mouse events to appropriate handlers
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.MouseClickMsg:
		if msg.Button == tea.MouseLeft {
			const headerLines = 2 // header card + blank line
			vpHeight := m.viewport.Height()
			vpY := msg.Y - headerLines
			if vpY >= 0 && vpY < vpHeight {
				contentLine := vpY + m.viewport.YOffset()
				for _, zone := range m.cardZones {
					if contentLine >= zone.startLine && contentLine <= zone.endLine {
						if m.expandedCards[zone.id] {
							delete(m.expandedCards, zone.id)
						} else {
							m.expandedCards[zone.id] = true
						}
						m.refreshViewport()
						break
					}
				}
			}
		}
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

	return tea.Batch(cmds...)
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
	m.lastCacheRead = resp.Result.Usage.CacheReadInputTokens
	m.lastCacheCreation = resp.Result.Usage.CacheCreationInputTokens
	m.lastDurationMs = resp.Result.DurationMs
	m.lastAPIMs = resp.Result.DurationAPIMs
}

// SetSize updates the chat tab dimensions.
func (m *ChatModel) SetSize(w, h int) {
	m.width = w
	m.height = h

	innerW := w - m.padH*2
	if innerW < 20 {
		innerW = 20
	}
	textareaHeight := 3
	// 1 header + 1 blank line below header + 1 divider + 1 status line
	viewportHeight := h - textareaHeight - 4

	m.viewport.SetWidth(innerW)
	m.viewport.SetHeight(viewportHeight)
	m.textarea.SetWidth(innerW)
	m.textarea.SetHeight(textareaHeight)

	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(styles.DarkStyle),
		glamour.WithWordWrap(innerW-7),
	)
	if err == nil {
		m.renderer = r
	}
	m.refreshViewport()
}

// View renders the chat tab.
func (m *ChatModel) View() string {
	innerW := m.width - m.padH*2
	if innerW < 20 {
		innerW = 20
	}
	pad := strings.Repeat(" ", m.padH)

	// Sticky header card
	header := m.renderHeaderCard(innerW)

	// Divider between viewport and textarea
	var divider string
	if m.scrollMode {
		scrollStyle := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("3"))
		pct := int(m.viewport.ScrollPercent() * 100)
		label := fmt.Sprintf(" SCROLL (esc to exit) %d%% ", pct)
		labelW := lipgloss.Width(label)
		lineW := innerW - labelW
		if lineW < 0 {
			lineW = 0
		}
		divider = scrollStyle.Render(label) + lipgloss.NewStyle().
			Foreground(lipgloss.Color("3")).
			Render(strings.Repeat("─", lineW))
	} else {
		hint := lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Render(fmt.Sprintf(" ctrl+p: %s ", m.permMode.Short()))
		hintW := lipgloss.Width(hint)
		lineW := innerW - hintW
		if lineW < 0 {
			lineW = 0
		}
		divider = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")).
			Render(strings.Repeat("─", lineW)) + hint
	}

	// Indent every line with horizontal padding
	body := fmt.Sprintf("%s\n\n%s\n%s\n%s\n%s",
		header,
		m.viewport.View(),
		m.renderStatusLine(),
		divider,
		m.textarea.View(),
	)

	var sb strings.Builder
	for i, line := range strings.Split(body, "\n") {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(pad)
		sb.WriteString(line)
	}
	return sb.String()
}
