package ingress

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/viif/momu-llmgateway/internal/config"
)

func TestMiddlewareChainFullFlow(t *testing.T) {
	mr := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(RequestIDMiddleware())
	r.Use(LoggingMiddleware())
	r.Use(AuthMiddleware([]config.APIKeyConfig{
		{Key: "sk-test", Name: "test", RateLimit: 100, AllowedModels: []string{"*"}},
	}))
	r.Use(RateLimitMiddleware(redisClient))
	r.Use(ValidationMiddleware([]string{"gpt-4o", "gpt-4o-mini", "deepseek-chat"}))
	r.POST("/v1/chat/completions", func(c *gin.Context) {
		requestID, _ := c.Get("request_id")
		require.NotEmpty(t, requestID)
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	body, _ := json.Marshal(map[string]any{
		"model":    "gpt-4o",
		"messages": []map[string]string{{"role": "user", "content": "hello"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-test")
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.NotEmpty(t, w.Header().Get("X-Request-ID"))
}

func TestMiddlewareChainAuthFailsBeforeValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mr := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	r := gin.New()
	r.Use(RequestIDMiddleware())
	r.Use(LoggingMiddleware())
	r.Use(AuthMiddleware([]config.APIKeyConfig{
		{Key: "sk-test", Name: "test", RateLimit: 100, AllowedModels: []string{"gpt-4o"}},
	}))
	r.Use(RateLimitMiddleware(redisClient))
	r.Use(ValidationMiddleware([]string{"gpt-4o", "deepseek-chat"}))

	body, _ := json.Marshal(map[string]any{
		"model":    "gpt-4o",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestMiddlewareChainHealthBypassesAll(t *testing.T) {
	mr := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(RequestIDMiddleware())
	r.Use(LoggingMiddleware())
	r.Use(AuthMiddleware([]config.APIKeyConfig{
		{Key: "sk-test", Name: "test", RateLimit: 100, AllowedModels: []string{"*"}},
	}))
	r.Use(RateLimitMiddleware(redisClient))
	r.Use(ValidationMiddleware([]string{"gpt-4o"}))
	r.GET("/health", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.NotEmpty(t, w.Header().Get("X-Request-ID"))
}
