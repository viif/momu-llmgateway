package egress

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/viif/momu-llmgateway/internal/model"
)

type Anthropic struct {
	name    string
	baseURL string
	apiKey  string
	models  []string
	client  *http.Client
}

func NewAnthropic(name, baseURL, apiKey string, models []string, timeout time.Duration) *Anthropic {
	return &Anthropic{name: name, baseURL: baseURL, apiKey: apiKey, models: models, client: &http.Client{Timeout: timeout}}
}

func (p *Anthropic) Name() string { return p.name }

func (p *Anthropic) Models() []string { return p.models }

func (p *Anthropic) buildRequestBody(req *model.StandardRequest) ([]byte, error) {
	system := ""
	messages := make([]model.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == "system" {
			system = m.Content
			continue
		}
		messages = append(messages, m)
	}
	return json.Marshal(map[string]any{"model": req.Model, "system": system, "messages": messages, "max_tokens": req.MaxTokens})
}

func (p *Anthropic) Send(ctx context.Context, req *model.StandardRequest) (*model.StandardResponse, error) {
	body, err := p.buildRequestBody(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return nil, model.NewError(model.ErrCodeProviderError, resp.Status)
	}
	var raw struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := &model.StandardResponse{
		ID:       raw.ID,
		Model:    raw.Model,
		Provider: p.Name(),
		Usage: model.Usage{
			PromptTokens:     raw.Usage.InputTokens,
			CompletionTokens: raw.Usage.OutputTokens,
			TotalTokens:      raw.Usage.InputTokens + raw.Usage.OutputTokens,
		},
	}
	if len(raw.Content) > 0 {
		out.Choices = []model.Choice{
			{
				Index:        0,
				Message:      model.Message{Role: "assistant", Content: raw.Content[0].Text},
				FinishReason: "stop",
			},
		}
	}
	return out, nil
}

func (p *Anthropic) SendStream(ctx context.Context, req *model.StandardRequest) (<-chan model.StreamChunk, error) {
	return StreamAnthropic(ctx, p.client, p.baseURL+"/v1/messages", p.apiKey, req)
}

func parseAnthropicSSEEvent(eventType, data string) (model.StreamChunk, bool, error) {
	eventType = strings.TrimSpace(eventType)
	data = strings.TrimSpace(data)
	if eventType == "" && data == "" {
		return model.StreamChunk{}, false, nil
	}
	if !strings.HasPrefix(data, "{") {
		return model.StreamChunk{}, false, nil
	}
	var raw struct {
		Type    string `json:"type"`
		Message struct {
			ID    string `json:"id"`
			Role  string `json:"role"`
			Model string `json:"model"`
		} `json:"message"`
		Delta struct {
			Type       string `json:"type"`
			Text       string `json:"text"`
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
		Index int `json:"index"`
	}
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return model.StreamChunk{}, false, err
	}
	switch raw.Type {
	case "message_start":
		return model.StreamChunk{ID: raw.Message.ID, Model: raw.Message.Model, Delta: model.Delta{Role: raw.Message.Role}}, false, nil
	case "content_block_delta":
		if raw.Delta.Type == "text_delta" {
			return model.StreamChunk{Delta: model.Delta{Content: raw.Delta.Text}}, false, nil
		}
	case "message_stop":
		return model.StreamChunk{Done: true}, true, nil
	}
	return model.StreamChunk{}, false, nil
}

func StreamAnthropic(ctx context.Context, client *http.Client, url, apiKey string, req *model.StandardRequest) (<-chan model.StreamChunk, error) {
	system := ""
	messages := make([]model.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == "system" {
			system = m.Content
			continue
		}
		messages = append(messages, m)
	}
	body, err := json.Marshal(map[string]any{
		"model":      req.Model,
		"system":     system,
		"messages":   messages,
		"max_tokens": req.MaxTokens,
		"stream":     true,
	})
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		_ = resp.Body.Close()
		return nil, model.NewError(model.ErrCodeProviderError, resp.Status)
	}
	out := make(chan model.StreamChunk)
	go func() {
		defer close(out)
		defer func() { _ = resp.Body.Close() }()
		scanner := bufio.NewScanner(resp.Body)
		var currentEvent string
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "event:") {
				currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
				continue
			}
			if strings.HasPrefix(line, "data:") {
				chunk, done, scanErr := parseAnthropicSSEEvent(currentEvent, strings.TrimPrefix(line, "data:"))
				currentEvent = ""
				if scanErr != nil {
					out <- model.StreamChunk{Error: model.NewError(model.ErrCodeProviderError, scanErr.Error())}
					return
				}
				if done {
					out <- chunk
					return
				}
				if chunk.ID != "" || chunk.Delta.Content != "" || chunk.Delta.Role != "" {
					out <- chunk
				}
			}
		}
		if err := scanner.Err(); err != nil {
			out <- model.StreamChunk{Error: model.NewError(model.ErrCodeProviderError, err.Error())}
		}
	}()
	return out, nil
}
