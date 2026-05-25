package ingress

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequestIDMiddlewareGeneratesAndSetsHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestIDMiddleware())
	r.GET("/ok", func(c *gin.Context) {
		id, exists := c.Get("request_id")
		require.True(t, exists)
		require.NotEmpty(t, id)
		c.Status(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNoContent, w.Code)
	require.NotEmpty(t, w.Header().Get("X-Request-ID"))
}

func TestRequestIDMiddlewareMultipleRequestsUnique(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestIDMiddleware())
	r.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })
	ids := map[string]bool{}
	for i := 0; i < 10; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ok", nil))
		id := w.Header().Get("X-Request-ID")
		require.NotEmpty(t, id)
		require.False(t, ids[id], "duplicate request id")
		ids[id] = true
	}
}
