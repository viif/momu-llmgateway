package ingress

import (
	"context"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/viif/momu-llmgateway/internal/model"
	"github.com/viif/momu-llmgateway/internal/observability"
)

func RegisterRoutes(r *gin.Engine, svc ChatService) {
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))
	r.POST("/v1/chat/completions", chatCompletionHandler(svc))
}

func chatCompletionHandler(svc ChatService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		requestID, _ := c.Get("request_id")
		if rid, ok := requestID.(string); ok {
			ctx = observability.WithRequestID(ctx, rid)
		}

		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": model.NewError(model.ErrCodeInvalidRequest, "failed to read body")})
			return
		}

		req, err := model.ParseStandardRequest(body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": model.NewError(model.ErrCodeInvalidRequest, "invalid request body")})
			return
		}

		if rid, ok := requestID.(string); ok {
			req.RequestID = rid
		}

		if svc == nil {
			c.JSON(http.StatusNotImplemented, gin.H{"error": "chat service not wired"})
			return
		}

		if req.Stream {
			handleStream(c, ctx, svc, req)
			return
		}

		handleNonStreaming(c, ctx, svc, req)
	}
}

func handleNonStreaming(c *gin.Context, ctx context.Context, svc ChatService, req *model.StandardRequest) {
	resp, err := svc.HandleChatCompletion(ctx, req)
	if err != nil {
		statusCode := errorToHTTPStatus(err)
		c.JSON(statusCode, gin.H{"error": err.(*model.Error)})
		return
	}

	if resp.CacheHit {
		c.Header("X-Cache", "HIT")
	}
	c.JSON(http.StatusOK, resp)
}

func handleStream(c *gin.Context, ctx context.Context, svc ChatService, req *model.StandardRequest) {
	ch, err := svc.HandleChatCompletionStream(ctx, req)
	if err != nil {
		statusCode := errorToHTTPStatus(err)
		c.JSON(statusCode, gin.H{"error": err.(*model.Error)})
		return
	}

	c.Status(http.StatusOK)
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": model.NewError(model.ErrCodeInternal, "streaming not supported")})
		return
	}

	for chunk := range ch {
		if chunk.Error != nil {
			_ = writeSSEEvent(c.Writer, chunk)
			flusher.Flush()
			return
		}
		_ = writeSSEEvent(c.Writer, chunk)
		flusher.Flush()
	}
	_ = writeSSEDone(c.Writer)
	flusher.Flush()
}

func writeSSEEvent(w io.Writer, chunk model.StreamChunk) error {
	data, err := chunk.ToJSON()
	if err != nil {
		return err
	}
	_, err = w.Write(append([]byte("data: "), append(data, '\n', '\n')...))
	return err
}

func writeSSEDone(w io.Writer) error {
	_, err := w.Write([]byte("data: [DONE]\n\n"))
	return err
}

func errorToHTTPStatus(err error) int {
	if me, ok := err.(*model.Error); ok {
		switch me.Code {
		case model.ErrCodeInvalidRequest, model.ErrCodeModelNotFound:
			return http.StatusBadRequest
		case model.ErrCodeAuthentication:
			return http.StatusUnauthorized
		case model.ErrCodeRateLimit:
			return http.StatusTooManyRequests
		case model.ErrCodeCircuitOpen:
			return http.StatusServiceUnavailable
		case model.ErrCodeProviderError, model.ErrCodeTimeout:
			return http.StatusBadGateway
		case model.ErrCodeFallbackExhausted:
			return http.StatusServiceUnavailable
		default:
			return http.StatusInternalServerError
		}
	}
	return http.StatusInternalServerError
}
