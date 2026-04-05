package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const defaultAnthropicURL = "https://api.anthropic.com"
const anthropicVersion = "2023-06-01"

type AnthropicConfig struct {
	APIKey  string
	BaseURL string
}

type Anthropic struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

func NewAnthropic(cfg AnthropicConfig) (*Anthropic, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY not set")
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultAnthropicURL
	}
	return &Anthropic{
		apiKey:  cfg.APIKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{},
	}, nil
}

func (a *Anthropic) ID() string          { return "anthropic" }
func (a *Anthropic) SupportsTools() bool { return true }

// retryableStatus returns true for HTTP status codes that should be retried.
func retryableStatus(code int) bool {
	return code == 429 || code == 500 || code == 502 || code == 503 || code == 529
}

// retryDelay returns how long to wait before retrying. Uses Retry-After header if present,
// otherwise falls back to exponential backoff.
func retryDelay(resp *http.Response, attempt int) time.Duration {
	if resp != nil {
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, err := strconv.Atoi(ra); err == nil {
				return time.Duration(secs) * time.Second
			}
		}
	}
	delays := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}
	if attempt < len(delays) {
		return delays[attempt]
	}
	return delays[len(delays)-1]
}

func (a *Anthropic) Stream(ctx context.Context, req *Request) <-chan StreamEvent {
	ch := make(chan StreamEvent, 32)

	go func() {
		defer close(ch)

		body := a.buildRequestBody(req)
		jsonBody, err := json.Marshal(body)
		if err != nil {
			ch <- EventError{Err: fmt.Errorf("marshaling request: %w", err)}
			return
		}

		const maxRetries = 3
		var resp *http.Response

		for attempt := 0; attempt <= maxRetries; attempt++ {
			httpReq, err := http.NewRequestWithContext(ctx, "POST", a.baseURL+"/v1/messages", bytes.NewReader(jsonBody))
			if err != nil {
				ch <- EventError{Err: fmt.Errorf("creating request: %w", err)}
				return
			}

			httpReq.Header.Set("Content-Type", "application/json")
			httpReq.Header.Set("x-api-key", a.apiKey)
			httpReq.Header.Set("anthropic-version", anthropicVersion)

			resp, err = a.client.Do(httpReq)
			if err != nil {
				if ctx.Err() != nil {
					ch <- EventError{Err: ctx.Err()}
					return
				}
				if attempt < maxRetries {
					time.Sleep(retryDelay(nil, attempt))
					continue
				}
				ch <- EventError{Err: fmt.Errorf("API request failed: %w", err)}
				return
			}

			if resp.StatusCode == http.StatusOK {
				break
			}

			bodyBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if retryableStatus(resp.StatusCode) && attempt < maxRetries {
				delay := retryDelay(resp, attempt)
				// Don't emit EventError during retry — it would reset TUI state.
				// Just wait and retry silently. The spinner keeps spinning.
				select {
				case <-ctx.Done():
					ch <- EventError{Err: ctx.Err()}
					return
				case <-time.After(delay):
					continue
				}
			}

			ch <- EventError{Err: fmt.Errorf("API error %d: %s", resp.StatusCode, string(bodyBytes))}
			return
		}

		defer resp.Body.Close()
		a.processStream(resp.Body, ch)
	}()

	return ch
}

type anthropicRequest struct {
	Model     string            `json:"model"`
	MaxTokens int               `json:"max_tokens"`
	Stream    bool              `json:"stream"`
	System    string            `json:"system,omitempty"`
	Messages  []json.RawMessage `json:"messages"`
	Tools     []ToolDefinition  `json:"tools,omitempty"`
}

func (a *Anthropic) buildRequestBody(req *Request) anthropicRequest {
	messages := make([]json.RawMessage, 0, len(req.Messages))
	for _, msg := range req.Messages {
		data, _ := json.Marshal(msg)
		messages = append(messages, data)
	}

	return anthropicRequest{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		Stream:    true,
		System:    req.System,
		Messages:  messages,
		Tools:     req.Tools,
	}
}

func (a *Anthropic) processStream(body io.Reader, ch chan<- StreamEvent) {
	var currentToolID string
	var currentToolName string
	var toolInputBuf strings.Builder
	var inputTokens int

	for sse := range ParseSSE(body) {
		switch sse.Event {
		case "content_block_start":
			var block struct {
				ContentBlock struct {
					Type string `json:"type"`
					ID   string `json:"id"`
					Name string `json:"name"`
					Text string `json:"text"`
				} `json:"content_block"`
			}
			if err := json.Unmarshal([]byte(sse.Data), &block); err != nil {
				continue
			}
			if block.ContentBlock.Type == "tool_use" {
				currentToolID = block.ContentBlock.ID
				currentToolName = block.ContentBlock.Name
				toolInputBuf.Reset()
				ch <- EventToolUseStart{
					ID:   currentToolID,
					Name: currentToolName,
				}
			}

		case "content_block_delta":
			var delta struct {
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
			}
			if err := json.Unmarshal([]byte(sse.Data), &delta); err != nil {
				continue
			}
			switch delta.Delta.Type {
			case "text_delta":
				ch <- EventTextDelta{Text: delta.Delta.Text}
			case "input_json_delta":
				toolInputBuf.WriteString(delta.Delta.PartialJSON)
				ch <- EventToolUseDelta{PartialJSON: delta.Delta.PartialJSON}
			}

		case "content_block_stop":
			if currentToolID != "" {
				inputJSON := json.RawMessage(toolInputBuf.String())
				if !json.Valid(inputJSON) {
					inputJSON = json.RawMessage("{}")
				}
				ch <- EventToolUseEnd{
					ID:    currentToolID,
					Name:  currentToolName,
					Input: inputJSON,
				}
				currentToolID = ""
				currentToolName = ""
				toolInputBuf.Reset()
			}

		case "message_delta":
			var delta struct {
				Delta struct {
					StopReason string `json:"stop_reason"`
				} `json:"delta"`
				Usage Usage `json:"usage"`
			}
			if err := json.Unmarshal([]byte(sse.Data), &delta); err != nil {
				continue
			}
			usage := delta.Usage
			usage.InputTokens += inputTokens
			ch <- EventMessageComplete{
				StopReason: delta.Delta.StopReason,
				Usage:      usage,
			}

		case "message_start":
			var msg struct {
				Message struct {
					Usage Usage `json:"usage"`
				} `json:"message"`
			}
			if err := json.Unmarshal([]byte(sse.Data), &msg); err == nil {
				inputTokens = msg.Message.Usage.InputTokens
			}

		case "error":
			ch <- EventError{Err: fmt.Errorf("stream error: %s", sse.Data)}
			return
		}
	}
}
