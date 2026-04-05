package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

const defaultOllamaURL = "http://127.0.0.1:11434"

type OllamaConfig struct {
	BaseURL string
}

type Ollama struct {
	baseURL string
	client  *http.Client
}

type ollamaChatRequest struct {
	Model    string                 `json:"model"`
	Messages []ollamaMessage        `json:"messages"`
	Tools    []ollamaToolDefinition `json:"tools,omitempty"`
	Stream   bool                   `json:"stream"`
	Options  *ollamaOptions         `json:"options,omitempty"`
}

type ollamaOptions struct {
	NumPredict int `json:"num_predict,omitempty"`
}

type ollamaToolDefinition struct {
	Type     string                `json:"type"`
	Function ollamaToolDefinitionF `json:"function"`
}

type ollamaToolDefinitionF struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Parameters  interface{} `json:"parameters,omitempty"`
}

type ollamaMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content,omitempty"`
	ToolName  string           `json:"tool_name,omitempty"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
}

type ollamaToolCall struct {
	Type     string           `json:"type,omitempty"`
	Function ollamaToolCallFn `json:"function"`
}

type ollamaToolCallFn struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

type ollamaStreamChunk struct {
	Message struct {
		Role      string           `json:"role"`
		Content   string           `json:"content"`
		ToolCalls []ollamaToolCall `json:"tool_calls"`
	} `json:"message"`
	Done            bool   `json:"done"`
	DoneReason      string `json:"done_reason"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
	Error           string `json:"error"`
}

type ollamaErrorResponse struct {
	Error string `json:"error"`
}

func NewOllama(cfg OllamaConfig) (*Ollama, error) {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultOllamaURL
	}
	return &Ollama{
		baseURL: baseURL,
		client:  &http.Client{},
	}, nil
}

func (o *Ollama) ID() string          { return "ollama" }
func (o *Ollama) SupportsTools() bool { return true }

func (o *Ollama) Stream(ctx context.Context, req *Request) <-chan StreamEvent {
	ch := make(chan StreamEvent, 32)

	go func() {
		defer close(ch)

		body := o.buildRequestBody(req)
		jsonBody, err := json.Marshal(body)
		if err != nil {
			ch <- EventError{Err: fmt.Errorf("marshaling request: %w", err)}
			return
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/chat", bytes.NewReader(jsonBody))
		if err != nil {
			ch <- EventError{Err: fmt.Errorf("creating request: %w", err)}
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := o.client.Do(httpReq)
		if err != nil {
			if ctx.Err() != nil {
				ch <- EventError{Err: ctx.Err()}
				return
			}
			ch <- EventError{Err: fmt.Errorf("reaching Ollama at %s: %w\n\nStart it with:\n  ollama serve", o.baseURL, err)}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			ch <- EventError{Err: o.readAPIError(resp)}
			return
		}

		if err := o.processStream(resp.Body, ch); err != nil {
			if ctx.Err() != nil || errors.Is(err, io.EOF) {
				return
			}
			ch <- EventError{Err: err}
		}
	}()

	return ch
}

func (o *Ollama) buildRequestBody(req *Request) ollamaChatRequest {
	messages := convertMessagesForOllama(req.System, req.Messages)
	tools := make([]ollamaToolDefinition, 0, len(req.Tools))
	for _, tool := range req.Tools {
		var parameters interface{}
		if len(tool.InputSchema) > 0 {
			_ = json.Unmarshal(tool.InputSchema, &parameters)
		}
		tools = append(tools, ollamaToolDefinition{
			Type: "function",
			Function: ollamaToolDefinitionF{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  parameters,
			},
		})
	}

	body := ollamaChatRequest{
		Model:    req.Model,
		Messages: messages,
		Tools:    tools,
		Stream:   true,
	}
	if req.MaxTokens > 0 {
		body.Options = &ollamaOptions{NumPredict: req.MaxTokens}
	}
	return body
}

func (o *Ollama) processStream(body io.Reader, ch chan<- StreamEvent) error {
	dec := json.NewDecoder(body)
	sawToolCall := false

	for {
		var chunk ollamaStreamChunk
		if err := dec.Decode(&chunk); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("decoding Ollama stream: %w", err)
		}

		if chunk.Error != "" {
			return fmt.Errorf("Ollama error: %s", chunk.Error)
		}

		if text := chunk.Message.Content; text != "" {
			ch <- EventTextDelta{Text: text}
		}

		for _, call := range chunk.Message.ToolCalls {
			sawToolCall = true
			callID := uuid.NewString()
			args, err := json.Marshal(call.Function.Arguments)
			if err != nil || !json.Valid(args) {
				args = json.RawMessage(`{}`)
			}
			ch <- EventToolUseStart{ID: callID, Name: call.Function.Name}
			ch <- EventToolUseEnd{
				ID:    callID,
				Name:  call.Function.Name,
				Input: json.RawMessage(args),
			}
		}

		if chunk.Done {
			stopReason := chunk.DoneReason
			if sawToolCall && stopReason == "" {
				stopReason = "tool_use"
			}
			ch <- EventMessageComplete{
				StopReason: stopReason,
				Usage: Usage{
					InputTokens:  chunk.PromptEvalCount,
					OutputTokens: chunk.EvalCount,
				},
			}
			return nil
		}
	}
}

func (o *Ollama) readAPIError(resp *http.Response) error {
	bodyBytes, _ := io.ReadAll(resp.Body)

	var apiErr ollamaErrorResponse
	if err := json.Unmarshal(bodyBytes, &apiErr); err == nil && apiErr.Error != "" {
		if strings.Contains(apiErr.Error, "not found") {
			return fmt.Errorf("%s\n\nPull the model first:\n  ollama pull %s", apiErr.Error, extractMissingModel(apiErr.Error))
		}
		return fmt.Errorf("Ollama API error %d: %s", resp.StatusCode, apiErr.Error)
	}

	return fmt.Errorf("Ollama API error %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
}

func extractMissingModel(msg string) string {
	parts := strings.Split(msg, "'")
	if len(parts) >= 2 {
		return parts[1]
	}
	return "<model>"
}

func convertMessagesForOllama(system string, messages []Message) []ollamaMessage {
	out := make([]ollamaMessage, 0, len(messages)+1)
	toolNamesByID := make(map[string]string)

	if system != "" {
		out = append(out, ollamaMessage{
			Role:    "system",
			Content: system,
		})
	}

	for _, msg := range messages {
		parts := splitContentBlocks(msg.Content)
		if len(parts.toolResults) > 0 {
			for _, result := range parts.toolResults {
				out = append(out, ollamaMessage{
					Role:     "tool",
					ToolName: toolNamesByID[result.ToolUseID],
					Content:  result.Content,
				})
			}
			continue
		}

		ollamaMsg := ollamaMessage{
			Role:    msg.Role,
			Content: parts.text,
		}
		if len(parts.toolUses) > 0 {
			ollamaMsg.Role = "assistant"
			ollamaMsg.ToolCalls = make([]ollamaToolCall, 0, len(parts.toolUses))
			for _, toolUse := range parts.toolUses {
				toolNamesByID[toolUse.ID] = toolUse.Name
				var arguments map[string]interface{}
				_ = json.Unmarshal(toolUse.Input, &arguments)
				ollamaMsg.ToolCalls = append(ollamaMsg.ToolCalls, ollamaToolCall{
					Type: "function",
					Function: ollamaToolCallFn{
						Name:      toolUse.Name,
						Arguments: arguments,
					},
				})
			}
		}

		out = append(out, ollamaMsg)
	}

	return out
}

type normalizedContent struct {
	text        string
	toolUses    []ToolUseBlock
	toolResults []ToolResultBlock
}

func splitContentBlocks(content []interface{}) normalizedContent {
	var parts normalizedContent

	for _, block := range content {
		switch b := block.(type) {
		case TextBlock:
			parts.text += b.Text
		case ToolUseBlock:
			parts.toolUses = append(parts.toolUses, b)
		case ToolResultBlock:
			parts.toolResults = append(parts.toolResults, b)
		case map[string]interface{}:
			blockType, _ := b["type"].(string)
			switch blockType {
			case "text":
				if text, ok := b["text"].(string); ok {
					parts.text += text
				}
			case "tool_use":
				parts.toolUses = append(parts.toolUses, ToolUseBlock{
					Type:  "tool_use",
					ID:    stringValue(b["id"]),
					Name:  stringValue(b["name"]),
					Input: rawJSONValue(b["input"]),
				})
			case "tool_result":
				parts.toolResults = append(parts.toolResults, ToolResultBlock{
					Type:      "tool_result",
					ToolUseID: stringValue(b["tool_use_id"]),
					Content:   stringValue(b["content"]),
					IsError:   boolValue(b["is_error"]),
				})
			}
		}
	}

	return parts
}

func stringValue(v interface{}) string {
	s, _ := v.(string)
	return s
}

func boolValue(v interface{}) bool {
	b, _ := v.(bool)
	return b
}

func rawJSONValue(v interface{}) json.RawMessage {
	if v == nil {
		return json.RawMessage(`{}`)
	}
	data, err := json.Marshal(v)
	if err != nil || !json.Valid(data) {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(data)
}
