package egress

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
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
	return nil, model.NewError(model.ErrCodeProviderError, "openai compat streaming adapter not wired yet")
}
