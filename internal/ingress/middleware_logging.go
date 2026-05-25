package ingress

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/viif/momu-llmgateway/internal/observability"
	"go.uber.org/zap"
)

func LoggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		latency := time.Since(start)
		status := c.Writer.Status()
		fields := []zap.Field{
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", status),
			zap.Duration("latency", latency),
			zap.Int64("content_length", c.Request.ContentLength),
		}
		if rid, exists := c.Get("request_id"); exists {
			fields = append(fields, zap.String("request_id", rid.(string)))
		}
		if status >= 400 {
			observability.Logger.Warn("request", fields...)
		} else {
			observability.Logger.Info("request", fields...)
		}
	}
}
