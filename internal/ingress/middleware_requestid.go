package ingress

import (
	"github.com/gin-gonic/gin"
	"github.com/viif/momu-llmgateway/internal/observability"
)

func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := observability.NewRequestID()
		c.Set("request_id", id)
		c.Header("X-Request-ID", id)
		c.Next()
	}
}
