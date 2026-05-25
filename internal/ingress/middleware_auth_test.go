package ingress

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/viif/momu-llmgateway/internal/config"
)

func TestAuthMiddlewareAcceptsBearerKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthMiddleware([]config.APIKeyConfig{
		{Key: "sk-test", Name: "test", RateLimit: 60, AllowedModels: []string{"*"}},
	}))
	r.POST("/v1/chat/completions", func(c *gin.Context) {
		name, _ := c.Get("api_key_name")
		require.Equal(t, "test", name)
		rl, _ := c.Get("api_key_rate_limit")
		require.Equal(t, 60, rl)
		c.Status(http.StatusOK)
	})
	body, _ := json.Marshal(map[string]any{"model": "gpt-4o", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-test")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestAuthMiddlewareRejectsMissingAuthorization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthMiddleware([]config.APIKeyConfig{{Key: "sk-test", Name: "test", AllowedModels: []string{"*"}}}))
	r.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ok", nil))
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddlewareRejectsInvalidKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthMiddleware([]config.APIKeyConfig{{Key: "sk-test", Name: "test", AllowedModels: []string{"*"}}}))
	r.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	req.Header.Set("Authorization", "Bearer sk-wrong")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddlewareRejectsNonBearerPrefix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthMiddleware([]config.APIKeyConfig{{Key: "sk-test", Name: "test", AllowedModels: []string{"*"}}}))
	r.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	req.Header.Set("Authorization", "Basic sk-test")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddlewareAllowedModelsEnforced(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthMiddleware([]config.APIKeyConfig{
		{Key: "sk-limited", Name: "limited", AllowedModels: []string{"gpt-4o-mini"}},
	}))
	r.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusOK) })
	body, _ := json.Marshal(map[string]any{"model": "gpt-4o", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-limited")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "model_not_allowed")
}

func TestAuthMiddlewareWildcardAllowsAnyModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthMiddleware([]config.APIKeyConfig{
		{Key: "sk-admin", Name: "admin", AllowedModels: []string{"*"}},
	}))
	r.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusOK) })
	body, _ := json.Marshal(map[string]any{"model": "gpt-4o", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-admin")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}
