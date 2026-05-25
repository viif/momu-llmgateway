package observability

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

func TestRegisterMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	RegisterMetrics(reg)
	_, err := reg.Gather()
	require.NoError(t, err)
}

func TestRequestTotalCounter(t *testing.T) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(RequestTotal)
	RequestTotal.WithLabelValues("openai", "gpt-4o", "success").Inc()
	RequestTotal.WithLabelValues("openai", "gpt-4o", "error").Inc()

	metricFamily, err := reg.Gather()
	require.NoError(t, err)
	found := false
	for _, mf := range metricFamily {
		if mf.GetName() == "llm_request_total" {
			found = true
			require.Len(t, mf.Metric, 2)
		}
	}
	require.True(t, found)
}

func TestCacheHitTotalCounter(t *testing.T) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(CacheHitTotal)
	CacheHitTotal.WithLabelValues("gpt-4o", "semantic").Inc()
	CacheHitTotal.WithLabelValues("gpt-4o", "semantic").Inc()

	metricFamily, err := reg.Gather()
	require.NoError(t, err)
	for _, mf := range metricFamily {
		if mf.GetName() == "llm_cache_hit_total" {
			require.Len(t, mf.Metric, 1)
			require.InDelta(t, 2.0, mf.Metric[0].GetCounter().GetValue(), 0.001)
		}
	}
}

func TestCircuitBreakerStateGauge(t *testing.T) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(CircuitBreakerState)
	CircuitBreakerState.WithLabelValues("openai", "gpt-4o").Set(1.0)
	CircuitBreakerState.WithLabelValues("openai", "gpt-4o").Set(0.0)

	metricFamily, err := reg.Gather()
	require.NoError(t, err)
	for _, mf := range metricFamily {
		if mf.GetName() == "llm_circuit_breaker_state" {
			require.Len(t, mf.Metric, 1)
			require.InDelta(t, 0.0, mf.Metric[0].GetGauge().GetValue(), 0.001)
		}
	}
}

func TestTokensTotalAndFallback(t *testing.T) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(TokensTotal, FallbackTotal)
	TokensTotal.WithLabelValues("openai", "gpt-4o", "prompt").Add(10)
	FallbackTotal.WithLabelValues("retry", "gpt-4o", "gpt-4o-mini").Inc()
}

func TestRequestDurationHistogram(t *testing.T) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(RequestDuration)
	RequestDuration.WithLabelValues("openai", "gpt-4o").Observe(0.5)
	RequestDuration.WithLabelValues("openai", "gpt-4o").Observe(1.0)

	metricFamily, err := reg.Gather()
	require.NoError(t, err)
	for _, mf := range metricFamily {
		if mf.GetName() == "llm_request_duration_seconds" {
			require.Len(t, mf.Metric, 1)
			require.InDelta(t, 2, mf.Metric[0].GetHistogram().GetSampleCount(), 0.001)
		}
	}
}
