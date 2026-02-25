package main

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

const maxCollapsedLines = 5

func (m *ChatModel) refreshViewport() {
	m.cardZones = m.cardZones[:0]
	var sb strings.Builder
	var lineCount int

	innerW := m.width - m.padH*2
	if innerW < 20 {
		innerW = 20
	}
	contentWidth := innerW - 4
	if contentWidth < 20 {
		contentWidth = 20
	}
	cardWidth := contentWidth
	if cardWidth < 20 {
		cardWidth = 20
	}

	// Init banner
	if m.initReceived {
		m.renderInitBanner(&sb, &lineCount)
	}

	for i, e := range m.entries {
		if i > 0 {
			sb.WriteString("\n")
			lineCount++
		}

		switch e.role {
		case "user":
			id := fmt.Sprintf("entry-%d", i)
			m.renderCard(&sb, &lineCount, id,
				m.styleUserLabel.Render("User:")+"\n"+e.text,
				m.styleUserCard, cardWidth, false)

		case "assistant":
			if len(e.blocks) > 0 {
				m.renderBlocks(&sb, e.blocks, contentWidth, false, i, &lineCount)
			} else if e.text != "" {
				brightWhite := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
				var displayText string
				if m.renderer != nil {
					rendered, err := m.renderer.Render(e.text)
					if err == nil {
						displayText = strings.TrimSpace(rendered)
					} else {
						displayText = brightWhite.Render(e.text)
					}
				} else {
					displayText = brightWhite.Render(e.text)
				}
				id := fmt.Sprintf("text-%d", i)
				m.renderCard(&sb, &lineCount, id, displayText,
					m.styleAssistantCard, cardWidth, false)
			}

			// Per-message metadata line
			if e.hasResult {
				meta := m.renderMessageMeta(e)
				sb.WriteString(meta)
				lineCount += strings.Count(meta, "\n")
			}

		case "error":
			id := fmt.Sprintf("entry-%d", i)
			m.renderCard(&sb, &lineCount, id, e.text,
				m.styleErrorCard, cardWidth, false)
		}
	}

	m.cachedContent = sb.String()
	m.cachedCardZoneCount = len(m.cardZones)
	m.cachedLineCount = lineCount
	wasAtBottom := m.viewport.AtBottom()
	m.viewport.SetContent(m.cachedContent)
	if wasAtBottom {
		m.viewport.GotoBottom()
	}
}

// renderCard renders content inside a styled card, with collapsible truncation.
// Cards longer than maxCollapsedLines are truncated unless expanded or forceExpanded.
func (m *ChatModel) renderCard(sb *strings.Builder, lineCount *int, id, content string,
	style lipgloss.Style, width int, forceExpanded bool,
) {
	startLine := *lineCount
	rendered := style.Width(width).Render(content)
	lines := strings.Split(rendered, "\n")

	expanded := forceExpanded || m.expandedCards[id]
	if !expanded && len(lines) > maxCollapsedLines {
		ellipsis := style.Width(width).Render(m.styleDim.Render("…"))
		ellipsisLine := strings.Split(ellipsis, "\n")[0]
		lines = append(lines[:maxCollapsedLines-1], ellipsisLine)
	}

	sb.WriteString(strings.Join(lines, "\n"))
	sb.WriteByte('\n')
	*lineCount += len(lines)

	endLine := *lineCount - 1
	m.cardZones = append(m.cardZones, cardZone{id: id, startLine: startLine, endLine: endLine})
}

// refreshStreamingViewport efficiently updates the viewport during streaming.
// It reuses the cached content for finalized entries and renders the streaming
// entry's blocks without glamour rendering.
func (m *ChatModel) refreshStreamingViewport() {
	// Preserve zones from finalized entries, discard streaming zones
	m.cardZones = m.cardZones[:m.cachedCardZoneCount]

	innerW := m.width - m.padH*2
	if innerW < 20 {
		innerW = 20
	}
	contentWidth := innerW - 4
	if contentWidth < 20 {
		contentWidth = 20
	}

	var sb strings.Builder
	var lineCount int

	// Init banner if no cached content yet
	if m.cachedContent == "" && m.initReceived {
		m.renderInitBanner(&sb, &lineCount)
	} else {
		// Use cached content for all finalized entries
		sb.WriteString(m.cachedContent)
		lineCount = m.cachedLineCount
	}

	// Append the streaming entry
	if len(m.entries) > 0 {
		last := &m.entries[len(m.entries)-1]
		if last.streaming {
			if sb.Len() > 0 {
				sb.WriteString("\n")
				lineCount++
			}

			// Render blocks incrementally (raw=true skips glamour, forces expanded)
			if len(last.blocks) > 0 {
				entryIdx := len(m.entries) - 1
				m.renderBlocks(&sb, last.blocks, contentWidth, true, entryIdx, &lineCount)
			}
			sb.WriteString("\n")
			lineCount++
		}
	}

	wasAtBottom := m.viewport.AtBottom()
	m.viewport.SetContent(sb.String())
	if wasAtBottom {
		m.viewport.GotoBottom()
	}
}

func (m *ChatModel) renderBlocks(sb *strings.Builder, blocks []ChatBlock,
	contentWidth int, raw bool, entryIdx int, lineCount *int,
) {
	// Group tool_use and tool_result by ToolID for inline rendering
	resultMap := make(map[string]*ChatBlock)
	for i := range blocks {
		if blocks[i].Kind == BlockToolResult {
			resultMap[blocks[i].ToolID] = &blocks[i]
		}
	}

	for blockIdx, block := range blocks {
		switch block.Kind {
		case BlockThinking:
			if block.Text == "" {
				continue
			}
			cardWidth := contentWidth
			if cardWidth < 20 {
				cardWidth = 20
			}
			label := m.styleThinkingLabel.Render("Thinking:")
			if raw {
				label = m.styleThinkingLabel.Render("Thinking ...")
			}
			id := fmt.Sprintf("block-%d-%d", entryIdx, blockIdx)
			m.renderCard(sb, lineCount, id, label+"\n"+block.Text,
				m.styleThinkingCard, cardWidth, raw)
			sb.WriteString("\n")
			*lineCount++

		case BlockText:
			if block.Text == "" {
				continue
			}
			cardWidth := contentWidth
			if cardWidth < 20 {
				cardWidth = 20
			}
			brightWhite := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
			var displayText string
			if !raw && m.renderer != nil {
				rendered, err := m.renderer.Render(block.Text)
				if err == nil {
					displayText = strings.TrimSpace(rendered)
				} else {
					displayText = brightWhite.Render(block.Text)
				}
			} else {
				displayText = brightWhite.Render(block.Text)
			}
			id := fmt.Sprintf("text-%d-%d", entryIdx, blockIdx)
			m.renderCard(sb, lineCount, id, displayText,
				m.styleAssistantCard, cardWidth, raw)
			sb.WriteString("\n")
			*lineCount++

		case BlockToolUse:
			cardWidth := contentWidth
			if cardWidth < 20 {
				cardWidth = 20
			}
			// Render tool content to buffer, then wrap in card
			innerWidth := contentWidth - 3 // account for card border + padding
			if innerWidth < 20 {
				innerWidth = 20
			}
			var toolBuf strings.Builder
			if block.IsTask {
				m.renderTaskBlock(&toolBuf, block, resultMap[block.ToolID], innerWidth)
			} else {
				m.renderCompactTool(&toolBuf, block, resultMap[block.ToolID], innerWidth)
			}
			id := fmt.Sprintf("tool-%d-%d", entryIdx, blockIdx)
			m.renderCard(sb, lineCount, id,
				strings.TrimRight(toolBuf.String(), "\n"),
				m.styleToolCard, cardWidth, false)
			sb.WriteString("\n")
			*lineCount++

		case BlockToolResult:
			continue
		}
	}
}

// renderMessageMeta formats the per-message metadata line.
func (m *ChatModel) renderMessageMeta(e chatEntry) string {
	var parts []string

	if e.model != "" {
		parts = append(parts, e.model)
	}

	parts = append(parts, fmt.Sprintf("$%.4f", e.result.CostUSD))

	parts = append(parts, fmt.Sprintf("%s out / %s in",
		formatTokens(e.result.Usage.OutputTokens),
		formatTokens(e.result.Usage.InputTokens)))

	// Cache hit ratio (total = non-cached + cached-read + cached-creation)
	totalIn := e.result.Usage.InputTokens + e.result.Usage.CacheReadInputTokens + e.result.Usage.CacheCreationInputTokens
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

	return m.styleDim.Render("  "+strings.Join(parts, " · ")) + "\n"
}

// renderInitBanner writes the init banner (model, version, tools, plugins) to sb.
func (m *ChatModel) renderInitBanner(sb *strings.Builder, lineCount *int) {
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
	text := m.styleDim.Render("  " + strings.Join(info, " · "))
	sb.WriteString(text)
	sb.WriteString("\n\n")
	*lineCount += strings.Count(text, "\n") + 2
}

// renderStatusLine builds the persistent status line below the textarea.
func (m *ChatModel) renderStatusLine() string {
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("238"))

	permStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("5"))

	if m.totalRequests == 0 && m.initReceived {
		info := m.initModel + " ready"
		return permStyle.Render(string(m.permMode)) + sepStyle.Render(" │ ") + dimStyle.Render(info)
	} else if m.totalRequests == 0 {
		return permStyle.Render(string(m.permMode)) + sepStyle.Render(" │ ") + dimStyle.Render("ready")
	}

	sep := sepStyle.Render(" │ ")
	var parts []string

	// Permission mode (first item, magenta)
	parts = append(parts, permStyle.Render(string(m.permMode)))

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
	// Cache hit ratio (total = non-cached + cached-read + cached-creation)
	totalIn := m.lastInputTok + m.lastCacheRead + m.lastCacheCreation
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

// shouldShowResultCard returns true if the tool result should be rendered
// as a dark-background card instead of a one-line summary.
func shouldShowResultCard(toolName, output string) bool {
	return toolName == "Bash" && strings.Contains(output, "\n")
}

// renderToolResultCard renders truncated tool output in a dark background card.
// The card spans nearly full content width, like opencode's output cards.
func (m *ChatModel) renderToolResultCard(sb *strings.Builder, output string, contentWidth int) {
	lines := strings.Split(output, "\n")
	maxLines := 15
	truncated := false
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		truncated = true
	}
	for i, line := range lines {
		lines[i] = truncateRunes(line, contentWidth-4)
	}
	cardContent := strings.Join(lines, "\n")
	if truncated {
		totalLines := len(strings.Split(output, "\n"))
		cardContent += "\n" + m.styleDim.Render(fmt.Sprintf("... (%d more lines)", totalLines-maxLines))
	}
	cardWidth := contentWidth
	if cardWidth < 20 {
		cardWidth = 20
	}
	sb.WriteString(m.styleToolResultCard.Width(cardWidth).Render(cardContent) + "\n")
}

// renderCompactTool renders a tool call in the compact style:
//
//	⚙ ToolName
//	  input summary
//	  ✓ first line of result
func (m *ChatModel) renderCompactTool(sb *strings.Builder, block ChatBlock, result *ChatBlock,
	contentWidth int,
) {
	maxLen := contentWidth - 6
	if maxLen < 20 {
		maxLen = 20
	}

	// Tool name + input summary on the same line
	inputLine := toolInputSummary(block.ToolName, block.ToolInput, maxLen)
	if inputLine != "" {
		sb.WriteString("  " + m.styleToolName.Render("⚙ "+block.ToolName) + " " + m.styleToolInput.Render(inputLine) + "\n")
	} else {
		sb.WriteString("  " + m.styleToolName.Render("⚙ "+block.ToolName) + "\n")
	}

	// Result
	if result != nil {
		if result.IsError {
			cleaned := cleanToolOutput(result.ToolOutput)
			outputLine := firstLine(cleaned, maxLen-4)
			sb.WriteString("    " + m.styleToolErr.Render("✗ "+outputLine) + "\n")
		} else if shouldShowResultCard(block.ToolName, result.ToolOutput) {
			m.renderToolResultCard(sb, result.ToolOutput, contentWidth)
		} else {
			cleaned := cleanToolOutput(result.ToolOutput)
			outputLine := firstLine(cleaned, maxLen-4)
			sb.WriteString("    " + m.styleDim.Render("✓ "+outputLine) + "\n")
		}
	}
}

// renderTaskBlock renders a Task (subagent) tool call with compact header,
// description, subagent activity, result, and metadata footer.
func (m *ChatModel) renderTaskBlock(sb *strings.Builder, block ChatBlock, result *ChatBlock,
	contentWidth int,
) {
	maxLen := contentWidth - 6
	if maxLen < 20 {
		maxLen = 20
	}

	// Header with subagent type
	label := block.TaskSubagentType
	if label == "" {
		label = "Task"
	}
	sb.WriteString("  " + m.styleToolName.Render("⚙ "+label) + "\n")

	// Description (one-liner, indented)
	if block.TaskDescription != "" {
		desc := truncateRunes(block.TaskDescription, maxLen)
		sb.WriteString("    " + m.styleToolInput.Render(desc) + "\n")
	}

	// Subagent activity (nested compact tool calls)
	if len(block.TaskSubBlocks) > 0 {
		sb.WriteString("    " + m.styleDim.Render("Activity:") + "\n")

		// Build result map for sub-blocks
		subResultMap := make(map[string]*ChatBlock)
		for i := range block.TaskSubBlocks {
			if block.TaskSubBlocks[i].Kind == BlockToolResult {
				subResultMap[block.TaskSubBlocks[i].ToolID] = &block.TaskSubBlocks[i]
			}
		}

		subMaxLen := maxLen - 4
		if subMaxLen < 20 {
			subMaxLen = 20
		}

		for _, sub := range block.TaskSubBlocks {
			if sub.Kind == BlockToolUse {
					// Tool name + input on same line
				inputLine := toolInputSummary(sub.ToolName, sub.ToolInput, subMaxLen)
				if inputLine != "" {
					sb.WriteString("      " + m.styleToolName.Render("⚙ "+sub.ToolName) + " " + m.styleToolInput.Render(inputLine) + "\n")
				} else {
					sb.WriteString("      " + m.styleToolName.Render("⚙ "+sub.ToolName) + "\n")
				}

				// Show first line of result
				if res, ok := subResultMap[sub.ToolID]; ok {
					marker := "✓"
					style := m.styleDim
					if res.IsError {
						marker = "✗"
						style = m.styleToolErr
					}
					cleaned := cleanToolOutput(res.ToolOutput)
					outputLine := firstLine(cleaned, subMaxLen-4)
					sb.WriteString("        " + style.Render(marker+" "+outputLine) + "\n")
				}
			}
		}
	}

	// Result
	if result != nil {
		if result.IsError {
			cleaned := cleanToolOutput(result.ToolOutput)
			outputLine := firstLine(cleaned, maxLen-4)
			sb.WriteString("    " + m.styleToolErr.Render("✗ "+outputLine) + "\n")
		} else {
			cleaned := cleanToolOutput(result.ToolOutput)
			outputLine := firstLine(cleaned, maxLen-4)
			sb.WriteString("    " + m.styleDim.Render("✓ "+outputLine) + "\n")
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
			sb.WriteString("    " + m.styleDim.Render(strings.Join(metaParts, " · ")) + "\n")
		}
	}
}

// renderHeaderCard renders the sticky header card showing conversation topic and stats.
func (m *ChatModel) renderHeaderCard(innerW int) string {
	topic := m.conversationTopic()
	titleStyle := lipgloss.NewStyle().Bold(true)
	statsStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))

	// Build stats: total tokens, cache%, cost
	var statParts []string
	totalTok := m.totalInputTok + m.totalOutputTok
	if totalTok > 0 {
		statParts = append(statParts, formatTokens(totalTok))
	}
	// Cache %
	totalIn := m.lastInputTok + m.lastCacheRead + m.lastCacheCreation
	if totalIn > 0 && m.lastCacheRead > 0 {
		pct := float64(m.lastCacheRead) / float64(totalIn) * 100
		statParts = append(statParts, fmt.Sprintf("%d%%", int(pct)))
	}
	statParts = append(statParts, fmt.Sprintf("($%.2f)", m.totalCost))
	stats := statsStyle.Render(strings.Join(statParts, "  "))

	// Compute widths — card has 1 padding each side from the style
	cardInnerW := innerW - 2 // account for PaddingLeft + PaddingRight
	if cardInnerW < 20 {
		cardInnerW = 20
	}
	statsW := lipgloss.Width(stats)
	titleMaxW := cardInnerW - statsW - 2
	if titleMaxW < 10 {
		titleMaxW = 10
	}
	title := titleStyle.Render(truncateRunes(topic, titleMaxW))
	titleW := lipgloss.Width(title)

	gap := cardInnerW - titleW - statsW
	if gap < 1 {
		gap = 1
	}
	content := title + strings.Repeat(" ", gap) + stats

	return m.styleHeaderCard.Width(innerW).Render(content)
}

// conversationTopic derives a short topic from the first user message.
func (m *ChatModel) conversationTopic() string {
	for _, e := range m.entries {
		if e.role == "user" {
			topic := strings.ReplaceAll(e.text, "\n", " ")
			topic = strings.TrimSpace(topic)
			if len(topic) > 60 {
				topic = topic[:57] + "..."
			}
			return topic
		}
	}
	if m.initModel != "" {
		return m.initModel
	}
	return "New conversation"
}
