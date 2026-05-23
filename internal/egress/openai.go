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

type OpenAICompatible struct {
	name    string
	baseURL string
	apiKey  string
	models  []string
	client  *http.Client
}

func NewOpenAICompatible(name, baseURL, apiKey string, models []string, timeout time.Duration) *OpenAICompatible {
	return &OpenAICompatible{name: name, baseURL: baseURL, apiKey: apiKey, models: models, client: &http.Client{Timeout: timeout}}
}

func (p *OpenAICompatible) Name() string { return p.name }

func (p *OpenAICompatible) Models() []string { return p.models }

func (p *OpenAICompatible) buildRequestBody(req *model.StandardRequest) ([]byte, error) {
	return json.Marshal(map[string]any{"model": req.Model, "messages": req.Messages, "stream": req.Stream, "temperature": req.Temperature, "max_tokens": req.MaxTokens})
}

func (p *OpenAICompatible) Send(ctx context.Context, req *model.StandardRequest) (*model.StandardResponse, error) {
	body, err := p.buildRequestBody(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, model.NewError(model.ErrCodeProviderError, resp.Status)
	}
	var out model.StandardResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	out.Provider = p.name
	return &out, nil
}

func (p *OpenAICompatible) SendStream(ctx context.Context, req *model.StandardRequest) (<-chan model.StreamChunk, error) {
	req.Stream = true
	body, err := p.buildRequestBody(req)
	if err != nil {
		return nil, err
	}
	return streamOpenAI(ctx, p.client, p.baseURL+"/chat/completions", p.apiKey, body)
}

func parseSSELine(line string) (model.StreamChunk, bool, error) {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "data:") {
		return model.StreamChunk{}, false, nil
	}
	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if payload == "[DONE]" {
		return model.StreamChunk{Done: true}, true, nil
	}
	var raw struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Delta model.Delta `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return model.StreamChunk{}, false, err
	}
	chunk := model.StreamChunk{ID: raw.ID, Model: raw.Model}
	if len(raw.Choices) > 0 {
		chunk.Delta = raw.Choices[0].Delta
	}
	return chunk, false, nil
}

func streamOpenAI(ctx context.Context, client *http.Client, url, apiKey string, body []byte) (<-chan model.StreamChunk, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, model.NewError(model.ErrCodeProviderError, resp.Status)
	}
	out := make(chan model.StreamChunk)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			chunk, done, scanErr := parseSSELine(scanner.Text())
			if scanErr != nil {
				out <- model.StreamChunk{Error: model.NewError(model.ErrCodeProviderError, scanErr.Error())}
				return
			}
			if done {
				out <- chunk
				return
			}
			if chunk.ID != "" || chunk.Delta.Content != "" {
				out <- chunk
			}
		}
		if err := scanner.Err(); err != nil {
			out <- model.StreamChunk{Error: model.NewError(model.ErrCodeProviderError, err.Error())}
		}
	}()
	return out, nil
}
