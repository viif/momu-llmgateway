package model

import "encoding/json"

type StandardRequest struct {
	RequestID   string            `json:"request_id,omitempty"`
	Model       string            `json:"model"`
	Messages    []Message         `json:"messages"`
	Stream      bool              `json:"stream,omitempty"`
	Temperature *float64          `json:"temperature,omitempty"`
	MaxTokens   *int              `json:"max_tokens,omitempty"`
	TaskType    string            `json:"task_type,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type StandardResponse struct {
	ID       string   `json:"id"`
	Model    string   `json:"model"`
	Provider string   `json:"provider,omitempty"`
	Choices  []Choice `json:"choices"`
	Usage    Usage    `json:"usage"`
	CacheHit bool     `json:"cache_hit,omitempty"`
}

type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type StreamChunk struct {
	ID    string `json:"id"`
	Model string `json:"model"`
	Delta Delta  `json:"delta"`
	Done  bool   `json:"done"`
	Error *Error `json:"error,omitempty"`
}

type Delta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

func (r *StandardResponse) ToJSON() ([]byte, error) {
	return json.Marshal(r)
}

func ParseStandardRequest(data []byte) (*StandardRequest, error) {
	var req StandardRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, err
	}
	return &req, nil
}
