package main

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

func (m *ChatModel) refreshViewport() {
	var sb strings.Builder

	contentWidth := m.width - 4
	if contentWidth < 20 {
		contentWidth = 20
	}

	separator := m.styleDim.Render(strings.Repeat("─", m.width))

	// Init banner
	if m.initReceived {
		m.renderInitBanner(&sb)
	}

	for i, e := range m.entries {
		if i > 0 {
			sb.WriteString("\n" + separator + "\n\n")
		}

		switch e.role {
		case "user":
			sb.WriteString(m.styleUserLabel.Render("YOU"))
			sb.WriteString("\n\n")
			sb.WriteString(e.text)
			sb.WriteString("\n")

		case "assistant":
			sb.WriteString(m.styleClaudeLabel.Render("CLAUDE"))
			sb.WriteString("\n")

			// Render thinking if present
			if e.thinking != "" {
				sb.WriteString("\n")
				sb.WriteString(m.styleThinkingLabel.Render("  Thinking"))
				sb.WriteString("\n")
				sb.WriteString(m.styleThinking.Render("  " + strings.ReplaceAll(e.thinking, "\n", "\n  ")))
				sb.WriteString("\n")
			}

			if len(e.blocks) > 0 {
				m.renderBlocks(&sb, e.blocks, contentWidth, false)
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
				sb.WriteString(m.renderMessageMeta(e))
			}

		case "error":
			sb.WriteString(m.styleErrLabel.Render("ERROR"))
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
	contentWidth := m.width - 4
	if contentWidth < 20 {
		contentWidth = 20
	}

	separator := m.styleDim.Render(strings.Repeat("─", m.width))

	var sb strings.Builder

	// Init banner if no cached content yet
	if m.cachedContent == "" && m.initReceived {
		m.renderInitBanner(&sb)
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
			sb.WriteString(m.styleClaudeLabel.Render("CLAUDE"))
			sb.WriteString("\n")

			if last.streamThinking != "" {
				sb.WriteString("\n")
				sb.WriteString(m.styleThinkingLabel.Render("  Thinking..."))
				sb.WriteString("\n")
				sb.WriteString(m.styleThinking.Render("  " + strings.ReplaceAll(last.streamThinking, "\n", "\n  ")))
				sb.WriteString("\n")
			}

			// Render blocks incrementally (raw=true skips glamour)
			if len(last.blocks) > 0 {
				m.renderBlocks(&sb, last.blocks, contentWidth, true)
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
	contentWidth int, raw bool,
) {
	// Group tool_use and tool_result by ToolID for inline rendering
	resultMap := make(map[string]*ChatBlock)
	for i := range blocks {
		if blocks[i].Kind == BlockToolResult {
			resultMap[blocks[i].ToolID] = &blocks[i]
		}
	}

	for _, block := range blocks {
		switch block.Kind {
		case BlockText:
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

		case BlockToolUse:
			sb.WriteString("\n")

			if block.IsTask {
				m.renderTaskBlock(sb, block, resultMap[block.ToolID], contentWidth)
				continue
			}

			// Compact rendering for Read tool calls
			if block.ToolName == "Read" {
				m.renderCompactTool(sb, block, resultMap[block.ToolID], contentWidth)
				continue
			}

			boxWidth := contentWidth - 2
			if boxWidth < 20 {
				boxWidth = 20
			}

			header := fmt.Sprintf("┌─ %s %s", m.styleToolName.Render("⚙ "+block.ToolName),
				m.styleToolBorder.Render(strings.Repeat("─", max(0, boxWidth-len(block.ToolName)-6))))
			sb.WriteString(m.styleToolBorder.Render("  ") + header + "\n")

			// Tool input
			inputLines := strings.Split(block.ToolInput, "\n")
			for _, line := range inputLines {
				line = truncateRunes(line, boxWidth-4)
				sb.WriteString(m.styleToolBorder.Render("  │ ") + m.styleToolInput.Render(line) + "\n")
			}

			// Tool result (if matched)
			if result, ok := resultMap[block.ToolID]; ok {
				sb.WriteString(m.styleToolBorder.Render("  ├─ "))
				if result.IsError {
					sb.WriteString(m.styleToolErr.Render("✗ Error") + "\n")
				} else {
					sb.WriteString(m.styleDim.Render("✓ Result") + "\n")
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
					line = truncateRunes(line, boxWidth-4)
					style := m.styleToolOutput
					if result.IsError {
						style = m.styleToolErr
					}
					sb.WriteString(m.styleToolBorder.Render("  │ ") + style.Render(line) + "\n")
				}
				if truncated {
					sb.WriteString(m.styleToolBorder.Render("  │ ") + m.styleDim.Render(fmt.Sprintf("... (%d more lines)", len(strings.Split(output, "\n"))-maxLines)) + "\n")
				}
			}

			// Close box
			footer := fmt.Sprintf("└%s", strings.Repeat("─", max(0, boxWidth)))
			sb.WriteString(m.styleToolBorder.Render("  "+footer) + "\n")

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
func (m *ChatModel) renderInitBanner(sb *strings.Builder) {
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
	sb.WriteString(m.styleDim.Render("  " + strings.Join(info, " · ")))
	sb.WriteString("\n\n")
}

// renderStatusLine builds the persistent status line below the textarea.
func (m *ChatModel) renderStatusLine() string {
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("238"))

	if m.totalRequests == 0 && m.initReceived {
		info := m.initModel + " ready"
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

// renderCompactTool renders a tool call in the compact subagent-activity style:
//
//	⚙ Read
//	  /path/to/file
//	  ✓ package main
func (m *ChatModel) renderCompactTool(sb *strings.Builder, block ChatBlock, result *ChatBlock,
	contentWidth int,
) {
	maxLen := contentWidth - 6
	if maxLen < 20 {
		maxLen = 20
	}

	sb.WriteString("  " + m.styleToolName.Render("⚙ "+block.ToolName) + "\n")

	// Input summary
	inputLine := toolInputSummary(block.ToolName, block.ToolInput, maxLen)
	if inputLine != "" {
		sb.WriteString("    " + m.styleToolInput.Render(inputLine) + "\n")
	}

	// Result summary
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
}

// renderTaskBlock renders a Task (subagent) tool call with header, subagent activity, result, and metadata footer.
func (m *ChatModel) renderTaskBlock(sb *strings.Builder, block ChatBlock, result *ChatBlock,
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
	header := fmt.Sprintf("┌─ %s %s", m.styleToolName.Render("⚙ "+label),
		m.styleToolBorder.Render(strings.Repeat("─", max(0, boxWidth-len(label)-6))))
	sb.WriteString(m.styleToolBorder.Render("  ") + header + "\n")

	// Description field
	renderTaskField(sb, "desc", block.TaskDescription, boxWidth, m.styleToolBorder, m.styleToolInput)

	// Prompt field (with wrapping)
	if block.TaskPrompt != "" {
		renderTaskField(sb, "prompt", block.TaskPrompt, boxWidth, m.styleToolBorder, m.styleToolInput)
	}

	// Subagent activity
	if len(block.TaskSubBlocks) > 0 {
		sb.WriteString(m.styleToolBorder.Render("  ├─ ") + m.styleDim.Render("Subagent Activity") + "\n")

		// Build result map for sub-blocks
		subResultMap := make(map[string]*ChatBlock)
		for i := range block.TaskSubBlocks {
			if block.TaskSubBlocks[i].Kind == BlockToolResult {
				subResultMap[block.TaskSubBlocks[i].ToolID] = &block.TaskSubBlocks[i]
			}
		}

		for _, sub := range block.TaskSubBlocks {
			if sub.Kind == BlockToolUse {
				sb.WriteString(m.styleToolBorder.Render("  │  ") + m.styleToolName.Render("⚙ "+sub.ToolName) + "\n")

				// Show meaningful summary of input
				inputLine := toolInputSummary(sub.ToolName, sub.ToolInput, boxWidth-8)
				if inputLine != "" {
					sb.WriteString(m.styleToolBorder.Render("  │    ") + m.styleToolInput.Render(inputLine) + "\n")
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
					outputLine := firstLine(cleaned, boxWidth-10)
					sb.WriteString(m.styleToolBorder.Render("  │    ") + style.Render(marker+" "+outputLine) + "\n")
				}
			}
		}
	}

	// Result (from the tool_result block)
	if result != nil {
		sb.WriteString(m.styleToolBorder.Render("  ├─ "))
		if result.IsError {
			sb.WriteString(m.styleToolErr.Render("✗ Error") + "\n")
		} else {
			sb.WriteString(m.styleDim.Render("✓ Result") + "\n")
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
			line = truncateRunes(line, boxWidth-4)
			style := m.styleToolOutput
			if result.IsError {
				style = m.styleToolErr
			}
			sb.WriteString(m.styleToolBorder.Render("  │ ") + style.Render(line) + "\n")
		}
		if truncated {
			sb.WriteString(m.styleToolBorder.Render("  │ ") + m.styleDim.Render(fmt.Sprintf("... (%d more lines)", len(strings.Split(output, "\n"))-maxLines)) + "\n")
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
			sb.WriteString(m.styleToolBorder.Render("  ├─ ") + m.styleDim.Render(strings.Join(metaParts, " · ")) + "\n")
		}
	}

	// Close box
	footer := fmt.Sprintf("└%s", strings.Repeat("─", max(0, boxWidth)))
	sb.WriteString(m.styleToolBorder.Render("  "+footer) + "\n")
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
