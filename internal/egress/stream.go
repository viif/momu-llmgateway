package egress

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/viif/momu-llmgateway/internal/model"
)

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

func StreamOpenAICompatible(ctx context.Context, client *http.Client, url, apiKey string, req *model.StandardRequest) (<-chan model.StreamChunk, error) {
	body, err := json.Marshal(map[string]any{"model": req.Model, "messages": req.Messages, "stream": true})
	if err != nil {
		return nil, err
	}
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
