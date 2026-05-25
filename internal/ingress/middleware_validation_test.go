package ingress

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

var testModels = []string{"gpt-4o", "gpt-4o-mini", "deepseek-chat"}

func TestValidationAcceptsValidRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ValidationMiddleware(testModels))
	r.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusOK) })

	body, _ := json.Marshal(map[string]any{
		"model":    "gpt-4o",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)))
	require.Equal(t, http.StatusOK, w.Code)
}

func TestValidationRejectsEmptyModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ValidationMiddleware(testModels))
	r.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusOK) })

	body, _ := json.Marshal(map[string]any{"messages": []map[string]string{{"role": "user", "content": "hi"}}})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "model")
}

func TestValidationRejectsUnknownModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ValidationMiddleware(testModels))
	r.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusOK) })

	body, _ := json.Marshal(map[string]any{
		"model":    "unknown-model",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "unknown model")
}

func TestValidationRejectsEmptyMessages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ValidationMiddleware(testModels))
	r.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusOK) })

	body, _ := json.Marshal(map[string]any{"model": "gpt-4o", "messages": []map[string]string{}})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "messages")
}

func TestValidationRejectsTemperatureOutOfRange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ValidationMiddleware(testModels))
	r.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusOK) })

	body, _ := json.Marshal(map[string]any{
		"model":       "gpt-4o",
		"messages":    []map[string]string{{"role": "user", "content": "hi"}},
		"temperature": 2.5,
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "temperature")
}

func TestValidationRejectsNegativeMaxTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ValidationMiddleware(testModels))
	r.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusOK) })

	body, _ := json.Marshal(map[string]any{
		"model":      "gpt-4o",
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
		"max_tokens": -1,
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "max_tokens")
}

func TestValidationSkipsNonChatPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ValidationMiddleware(testModels))
	r.GET("/health", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	require.Equal(t, http.StatusOK, w.Code)
}

func TestValidationAcceptsTemperatureBoundary(t *testing.T) {
	for _, temp := range []float64{0, 2} {
		gin.SetMode(gin.TestMode)
		r := gin.New()
		r.Use(ValidationMiddleware(testModels))
		r.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusOK) })

		body, _ := json.Marshal(map[string]any{
			"model":       "gpt-4o",
			"messages":    []map[string]string{{"role": "user", "content": "hi"}},
			"temperature": temp,
		})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)))
		require.Equal(t, http.StatusOK, w.Code)
	}
}


func TestValidationRejectsMalformedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ValidationMiddleware(testModels))
	r.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte("not json"))))
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestValidationRejectsMissingMessages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ValidationMiddleware(testModels))
	r.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusOK) })

	body, _ := json.Marshal(map[string]any{"model": "gpt-4o"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "messages")
}
