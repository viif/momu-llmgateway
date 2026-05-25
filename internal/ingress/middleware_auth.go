package ingress

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/viif/momu-llmgateway/internal/config"
)

func AuthMiddleware(keys []config.APIKeyConfig) gin.HandlerFunc {
	allowed := map[string]config.APIKeyConfig{}
	for _, k := range keys {
		allowed[k.Key] = k
	}
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if path == "/health" || path == "/metrics" {
			c.Next()
			return
		}
		token := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		cfg, ok := allowed[token]
		if !ok || token == "" || token == c.GetHeader("Authorization") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
			return
		}
		c.Set("api_key", token)
		c.Set("api_key_name", cfg.Name)
		c.Set("api_key_rate_limit", cfg.RateLimit)

		if !modelAllowed(cfg.AllowedModels, c) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error":   "model_not_allowed",
				"message": "requested model is not in the allowed models for this API key",
			})
			return
		}

		c.Next()
	}
}

func modelAllowed(allowedModels []string, c *gin.Context) bool {
	if len(allowedModels) == 1 && allowedModels[0] == "*" {
		return true
	}
	modelName := extractModelFromBody(c)
	if modelName == "" {
		return true
	}
	for _, m := range allowedModels {
		if m == modelName {
			return true
		}
	}
	return false
}

func extractModelFromBody(c *gin.Context) string {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return ""
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(body))
	var parsed struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	return parsed.Model
}
