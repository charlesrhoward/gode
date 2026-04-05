package provider

import (
	"context"
	"encoding/json"
)

// ContentBlock is the interface for message content blocks.
type ContentBlock interface {
	BlockType() string
}

type TextBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (b TextBlock) BlockType() string { return "text" }

type ToolUseBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

func (b ToolUseBlock) BlockType() string { return "tool_use" }

type ToolResultBlock struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

func (b ToolResultBlock) BlockType() string { return "tool_result" }

// Message is a conversation message.
type Message struct {
	Role    string        `json:"role"`
	Content []interface{} `json:"content"`
}

func UserTextMessage(text string) Message {
	return Message{
		Role:    "user",
		Content: []interface{}{TextBlock{Type: "text", Text: text}},
	}
}

func ToolResultMessage(toolUseID, content string, isError bool) Message {
	return Message{
		Role: "user",
		Content: []interface{}{ToolResultBlock{
			Type:      "tool_result",
			ToolUseID: toolUseID,
			Content:   content,
			IsError:   isError,
		}},
	}
}

// StreamEvent types emitted during streaming.
type StreamEvent interface {
	eventType() string
}

type EventTextDelta struct {
	Text string
}

func (e EventTextDelta) eventType() string { return "text_delta" }

type EventToolUseStart struct {
	ID   string
	Name string
}

func (e EventToolUseStart) eventType() string { return "tool_use_start" }

type EventToolUseDelta struct {
	PartialJSON string
}

func (e EventToolUseDelta) eventType() string { return "tool_use_delta" }

type EventToolUseEnd struct {
	ID    string
	Name  string
	Input json.RawMessage
}

func (e EventToolUseEnd) eventType() string { return "tool_use_end" }

type EventMessageComplete struct {
	StopReason string
	Usage      Usage
}

func (e EventMessageComplete) eventType() string { return "message_complete" }

type EventError struct {
	Err error
}

func (e EventError) eventType() string { return "error" }

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ToolDefinition is the schema sent to the API.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// Request is a provider API request.
type Request struct {
	Model     string
	Messages  []Message
	System    string
	Tools     []ToolDefinition
	MaxTokens int
}

// Provider is the LLM provider interface.
type Provider interface {
	ID() string
	SupportsTools() bool
	Stream(ctx context.Context, req *Request) <-chan StreamEvent
}
