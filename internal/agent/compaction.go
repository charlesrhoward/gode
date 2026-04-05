package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/tradecraft/gode/internal/provider"
	"github.com/tradecraft/gode/internal/session"
)

const (
	minMessagesToKeep        = 4
	minSummaryTokens         = 384
	maxSummaryTokens         = 2048
	minRecentTokenTarget     = 512
	defaultFallbackCtxTokens = 32768
)

func (a *Agent) maybeCompact(ctx context.Context, trigger string, force bool) (*EventCompacted, error) {
	budget := a.memoryBudgetTokens()
	before := a.estimateConversationTokens()

	if !force && before <= budget {
		return nil, nil
	}

	split := chooseCompactionSplit(a.messages, budget, force)
	if split <= 0 || split >= len(a.messages) {
		return nil, nil
	}

	newMemory, err := a.buildCompactedMemory(ctx, a.cfg.Session.Memory, a.messages[:split], budget)
	if err != nil {
		return nil, err
	}

	a.cfg.Session.Memory = strings.TrimSpace(newMemory)
	if err := a.cfg.Store.UpdateSessionMemory(a.cfg.Session.ID, a.cfg.Session.Memory); err != nil {
		return nil, fmt.Errorf("updating session memory: %w", err)
	}

	recentMessages := append([]provider.Message(nil), a.messages[split:]...)
	if err := session.ReplaceMessages(a.cfg.Store, a.cfg.Session.ID, recentMessages); err != nil {
		return nil, fmt.Errorf("rewriting compacted messages: %w", err)
	}
	a.messages = recentMessages

	event := &EventCompacted{
		Trigger:           trigger,
		BeforeTokens:      before,
		AfterTokens:       a.estimateConversationTokens(),
		BudgetTokens:      budget,
		CompactedMessages: split,
		RetainedMessages:  len(recentMessages),
	}
	a.events <- *event
	return event, nil
}

func (a *Agent) memoryBudgetTokens() int {
	if a.cfg.ContextTokens <= 0 {
		return defaultFallbackCtxTokens / 2
	}
	return a.cfg.ContextTokens / 2
}

func chooseCompactionSplit(messages []provider.Message, budget int, force bool) int {
	if len(messages) <= minMessagesToKeep {
		return 0
	}

	recentTarget := budget / 2
	if recentTarget < minRecentTokenTarget {
		recentTarget = minRecentTokenTarget
	}

	recentTokens := 0
	kept := 0
	split := len(messages)

	for i := len(messages) - 1; i >= 0; i-- {
		msgTokens := estimateMessageTokens(messages[i])
		if kept < minMessagesToKeep || recentTokens+msgTokens <= recentTarget {
			recentTokens += msgTokens
			kept++
			split = i
			continue
		}
		break
	}

	if split == 0 {
		if force {
			return len(messages) - minMessagesToKeep
		}
		return 0
	}

	return split
}

func (a *Agent) buildCompactedMemory(ctx context.Context, existingMemory string, compacted []provider.Message, budget int) (string, error) {
	summaryReq := &provider.Request{
		Model:     a.cfg.Model,
		System:    compactionSystemPrompt(),
		Messages:  []provider.Message{provider.UserTextMessage(buildCompactionPrompt(existingMemory, compacted))},
		MaxTokens: compactionMaxTokens(budget),
	}

	text, err := a.collectProviderText(ctx, summaryReq)
	if err != nil {
		return "", fmt.Errorf("building compacted memory: %w", err)
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("compaction returned an empty memory summary")
	}

	return text, nil
}

func (a *Agent) collectProviderText(ctx context.Context, req *provider.Request) (string, error) {
	var text strings.Builder
	stream := a.cfg.Provider.Stream(ctx, req)

	for evt := range stream {
		switch e := evt.(type) {
		case provider.EventTextDelta:
			text.WriteString(e.Text)
		case provider.EventMessageComplete:
			continue
		case provider.EventToolUseStart, provider.EventToolUseDelta, provider.EventToolUseEnd:
			continue
		case provider.EventError:
			return "", e.Err
		}
	}

	return text.String(), nil
}

func compactionSystemPrompt() string {
	return `You compress coding-assistant transcripts into durable working memory.

Rewrite the prior memory plus the transcript chunk into a fresh compact memory block.
Keep only information that will matter in later turns:
- user goals and constraints
- important codebase facts
- decisions already made
- files, symbols, and commands that matter
- unresolved bugs, TODOs, and next steps

Drop filler, repetition, and temporary back-and-forth.
Respond in concise markdown with these sections when relevant:
- Goals
- Repo Facts
- Decisions
- Open Work
- References`
}

func buildCompactionPrompt(existingMemory string, compacted []provider.Message) string {
	var b strings.Builder
	b.WriteString("Existing memory:\n")
	if strings.TrimSpace(existingMemory) == "" {
		b.WriteString("(none)\n")
	} else {
		b.WriteString(existingMemory)
		b.WriteString("\n")
	}
	b.WriteString("\nTranscript chunk to compact:\n\n")
	b.WriteString(formatMessagesForCompaction(compacted))
	return b.String()
}

func formatMessagesForCompaction(messages []provider.Message) string {
	var b strings.Builder
	for _, msg := range messages {
		role := strings.ToUpper(msg.Role)
		if role == "" {
			role = "UNKNOWN"
		}
		b.WriteString(role + ":\n")
		text := flattenMessageContent(msg.Content)
		if text == "" {
			text = "(empty)"
		}
		b.WriteString(text)
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String())
}

func flattenMessageContent(content []interface{}) string {
	var parts []string

	for _, block := range content {
		switch b := block.(type) {
		case provider.TextBlock:
			if strings.TrimSpace(b.Text) != "" {
				parts = append(parts, b.Text)
			}
		case provider.ToolUseBlock:
			parts = append(parts, fmt.Sprintf("[tool_use %s %s]", b.Name, strings.TrimSpace(string(b.Input))))
		case provider.ToolResultBlock:
			parts = append(parts, fmt.Sprintf("[tool_result %s]", strings.TrimSpace(b.Content)))
		case map[string]interface{}:
			blockType, _ := b["type"].(string)
			switch blockType {
			case "text":
				if text, ok := b["text"].(string); ok && strings.TrimSpace(text) != "" {
					parts = append(parts, text)
				}
			case "tool_use":
				name, _ := b["name"].(string)
				args, _ := json.Marshal(b["input"])
				parts = append(parts, fmt.Sprintf("[tool_use %s %s]", name, strings.TrimSpace(string(args))))
			case "tool_result":
				if text, ok := b["content"].(string); ok {
					parts = append(parts, fmt.Sprintf("[tool_result %s]", strings.TrimSpace(text)))
				}
			}
		}
	}

	return strings.Join(parts, "\n")
}

func estimateMessageTokens(msg provider.Message) int {
	return estimateTextTokens(flattenMessageContent(msg.Content)) + estimateTextTokens(msg.Role)
}

func estimateTextTokens(s string) int {
	if s == "" {
		return 0
	}
	return (utf8.RuneCountInString(s) + 3) / 4
}

func (a *Agent) estimateConversationTokens() int {
	total := estimateTextTokens(a.systemPrompt())
	for _, msg := range a.messages {
		total += estimateMessageTokens(msg)
	}
	return total
}

func compactionMaxTokens(budget int) int {
	tokens := budget / 4
	if tokens < minSummaryTokens {
		tokens = minSummaryTokens
	}
	if tokens > maxSummaryTokens {
		tokens = maxSummaryTokens
	}
	return tokens
}
