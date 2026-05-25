package ingress

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

func RateLimitMiddleware(client *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if path == "/health" || path == "/metrics" {
			c.Next()
			return
		}
		keyRaw, exists := c.Get("api_key")
		if !exists || client == nil {
			c.Next()
			return
		}
		key := keyRaw.(string)
		limitRaw, exists := c.Get("api_key_rate_limit")
		if !exists {
			c.Next()
			return
		}
		limit := limitRaw.(int)
		if limit <= 0 {
			c.Next()
			return
		}

		ctx := c.Request.Context()
		rateKey := fmt.Sprintf("ratelimit:%s", key)
		now := time.Now().UnixMilli()
		windowStart := now - 60_000

		pipe := client.Pipeline()
		pipe.ZRemRangeByScore(ctx, rateKey, "0", fmt.Sprintf("%d", windowStart))
		cardCmd := pipe.ZCard(ctx, rateKey)
		if _, err := pipe.Exec(ctx); err != nil {
			c.Next()
			return
		}

		if cardCmd.Err() != nil {
			c.Next()
			return
		}
		count := cardCmd.Val()
		if int(count) >= limit {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":   "rate_limit_exceeded",
				"message": fmt.Sprintf("rate limit exceeded: %d requests per minute", limit),
			})
			return
		}

		member := randomID(8)
		client.ZAdd(ctx, rateKey, redis.Z{Score: float64(now), Member: member})
		client.Expire(ctx, rateKey, 120*time.Second)

		c.Next()
	}
}

func randomID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
