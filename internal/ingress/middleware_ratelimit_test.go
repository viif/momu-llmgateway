package ingress

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func setupRateLimitRouter(client *redis.Client) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("api_key", "sk-test")
		c.Set("api_key_rate_limit", 3)
		c.Next()
	})
	r.Use(RateLimitMiddleware(client))
	r.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

func TestRateLimitAllowsUnderLimit(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	r := setupRateLimitRouter(client)

	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ok", nil))
		require.Equal(t, http.StatusOK, w.Code, "request %d should pass", i+1)
	}
}

func TestRateLimitBlocksOverLimit(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	r := setupRateLimitRouter(client)

	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ok", nil))
		if i < 3 {
			require.Equal(t, http.StatusOK, w.Code, "request %d should pass", i+1)
		} else {
			require.Equal(t, http.StatusTooManyRequests, w.Code, "request %d should be blocked", i+1)
		}
	}
}

func TestRateLimitResetsAfterWindow(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	r := setupRateLimitRouter(client)

	rateKey := "ratelimit:sk-test"
	oldTime := time.Now().Add(-61 * time.Second).UnixMilli()
	for i := 0; i < 3; i++ {
		client.ZAdd(context.Background(), rateKey, redis.Z{
			Score:  float64(oldTime),
			Member: fmt.Sprintf("old-%d", i),
		})
	}

	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ok", nil))
		require.Equal(t, http.StatusOK, w.Code, "request %d should pass", i+1)
	}
}

func TestRateLimitPerKeyIsolation(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		key := c.Request.Header.Get("X-Api-Key")
		c.Set("api_key", key)
		c.Set("api_key_rate_limit", 1)
		c.Next()
	})
	r.Use(RateLimitMiddleware(client))
	r.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })

	reqA := httptest.NewRequest(http.MethodGet, "/ok", nil)
	reqA.Header.Set("X-Api-Key", "key-a")
	reqB := httptest.NewRequest(http.MethodGet, "/ok", nil)
	reqB.Header.Set("X-Api-Key", "key-b")

	r.ServeHTTP(httptest.NewRecorder(), reqA)

	r.ServeHTTP(httptest.NewRecorder(), reqB)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, reqB)
	require.Equal(t, http.StatusTooManyRequests, w.Code, "key-b should exhaust its own limit")
}

func TestRateLimitMissingKeySkipsCheck(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RateLimitMiddleware(client))
	r.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ok", nil))
	require.Equal(t, http.StatusOK, w.Code)
}
