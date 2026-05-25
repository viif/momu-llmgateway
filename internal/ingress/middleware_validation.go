package ingress

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type chatRequest struct {
	Model       string         `json:"model"`
	Messages    []messageField `json:"messages"`
	Temperature *float64       `json:"temperature"`
	MaxTokens   *int           `json:"max_tokens"`
}

type messageField struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

func ValidationMiddleware(allowedModels []string) gin.HandlerFunc {
	modelSet := make(map[string]bool, len(allowedModels))
	for _, m := range allowedModels {
		modelSet[m] = true
	}
	return func(c *gin.Context) {
		if !strings.HasPrefix(c.Request.URL.Path, "/v1/chat/completions") || c.Request.Method != http.MethodPost {
			c.Next()
			return
		}

		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewBuffer(body))

		var req chatRequest
		if err := json.Unmarshal(body, &req); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
			return
		}

		if req.Model == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "model is required"})
			return
		}
		if !modelSet[req.Model] {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "unknown model: " + req.Model})
			return
		}
		if len(req.Messages) == 0 {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "messages must not be empty"})
			return
		}
		for _, msg := range req.Messages {
			if msg.Role == "" {
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "message role must not be empty"})
				return
			}
		}
		if req.Temperature != nil && (*req.Temperature < 0 || *req.Temperature > 2) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "temperature must be in range [0, 2]"})
			return
		}
		if req.MaxTokens != nil && *req.MaxTokens <= 0 {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "max_tokens must be a positive integer"})
			return
		}

		c.Next()
	}
}
