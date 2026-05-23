package observability

import "github.com/prometheus/client_golang/prometheus"

var RequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "llm_request_duration_seconds", Help: "LLM request latency"}, []string{"provider", "model"})
var RequestTotal = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "llm_request_total", Help: "LLM request count"}, []string{"provider", "model", "status"})
var TokensTotal = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "llm_tokens_total", Help: "LLM token count"}, []string{"provider", "model", "direction"})
var FallbackTotal = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "llm_fallback_total", Help: "Fallback count"}, []string{"level", "from_model", "to_model"})
var CircuitBreakerState = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "llm_circuit_breaker_state", Help: "Circuit breaker state"}, []string{"provider", "model"})
var CacheHitTotal = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "llm_cache_hit_total", Help: "Semantic cache hit count"}, []string{"model", "cache_type"})

func RegisterMetrics(reg *prometheus.Registry) {
	reg.MustRegister(RequestDuration, RequestTotal, TokensTotal, FallbackTotal, CircuitBreakerState, CacheHitTotal)
}
