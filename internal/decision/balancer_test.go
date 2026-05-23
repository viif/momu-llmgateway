package decision

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEffectiveWeightCalculation(t *testing.T) {
	cfg := BalancerConfig{
		ConcurrencyPenaltyCoefficient: 2.0,
		LatencyPenaltyCoefficient:     1.0,
	}
	b := NewBalancer(cfg)
	eff := b.EffectiveWeight(ProviderCandidate{
		ProviderName:         "a",
		BaseWeight:           100,
		ActiveConnections:    3,
		NormalizedP99Latency: 0.2,
		HealthScore:          0.9,
		WarmupFactor:         1.0,
	})
	expected := 100.0 * 1.0 * (1.0 / (1.0 + 2.0*3.0 + 1.0*0.2)) * 0.9
	require.InDelta(t, expected, eff, 0.001)
}

func TestEffectiveWeightWarmupReducesWeight(t *testing.T) {
	cfg := BalancerConfig{
		ConcurrencyPenaltyCoefficient: 0,
		LatencyPenaltyCoefficient:     0,
	}
	b := NewBalancer(cfg)
	full := b.EffectiveWeight(ProviderCandidate{BaseWeight: 100, HealthScore: 1, WarmupFactor: 1.0})
	warm := b.EffectiveWeight(ProviderCandidate{BaseWeight: 100, HealthScore: 1, WarmupFactor: 0.3})
	require.InDelta(t, 100.0, full, 0.001)
	require.InDelta(t, 30.0, warm, 0.001)
	require.Less(t, warm, full)
}

func TestSWRRDistributionFairness(t *testing.T) {
	cfg := BalancerConfig{
		ConcurrencyPenaltyCoefficient: 0,
		LatencyPenaltyCoefficient:     0,
	}
	b := NewBalancer(cfg)
	candidates := []ProviderCandidate{
		{ProviderName: "a", BaseWeight: 5, HealthScore: 1, WarmupFactor: 1},
		{ProviderName: "b", BaseWeight: 1, HealthScore: 1, WarmupFactor: 1},
		{ProviderName: "c", BaseWeight: 1, HealthScore: 1, WarmupFactor: 1},
	}
	counts := map[string]int{}
	for i := 0; i < 700; i++ {
		c := b.Select(candidates)
		counts[c.ProviderName]++
	}
	aPct := float64(counts["a"]) / 700.0
	require.InDelta(t, 5.0/7.0, aPct, 0.05)
}

func TestSelectEmptyCandidates(t *testing.T) {
	b := NewBalancer(BalancerConfig{})
	c := b.Select(nil)
	require.Equal(t, "", c.ProviderName)
}

func TestSelectSingleCandidate(t *testing.T) {
	b := NewBalancer(BalancerConfig{})
	c := b.Select([]ProviderCandidate{{ProviderName: "only", BaseWeight: 1, HealthScore: 1, WarmupFactor: 1}})
	require.Equal(t, "only", c.ProviderName)
}

func TestRegisterAssignsSequentialSlots(t *testing.T) {
	b := NewBalancer(BalancerConfig{})
	require.Equal(t, 0, b.Register("a"))
	require.Equal(t, 1, b.Register("b"))
	require.Equal(t, 0, b.Register("a"))
}

func TestSelectWithRegisteredProviders(t *testing.T) {
	cfg := BalancerConfig{
		ConcurrencyPenaltyCoefficient: 0,
		LatencyPenaltyCoefficient:     0,
	}
	b := NewBalancer(cfg)
	b.Register("a")
	b.Register("b")
	c := b.Select([]ProviderCandidate{
		{ProviderName: "a", BaseWeight: 1, HealthScore: 1, WarmupFactor: 1},
		{ProviderName: "b", BaseWeight: 100, HealthScore: 1, WarmupFactor: 1},
	})
	require.Equal(t, "b", c.ProviderName)
}

func TestRegisterThenSWRRFairness(t *testing.T) {
	cfg := BalancerConfig{
		ConcurrencyPenaltyCoefficient: 0,
		LatencyPenaltyCoefficient:     0,
	}
	b := NewBalancer(cfg)
	b.Register("a")
	b.Register("b")
	b.Register("c")
	candidates := []ProviderCandidate{
		{ProviderName: "a", BaseWeight: 5, HealthScore: 1, WarmupFactor: 1},
		{ProviderName: "b", BaseWeight: 1, HealthScore: 1, WarmupFactor: 1},
		{ProviderName: "c", BaseWeight: 1, HealthScore: 1, WarmupFactor: 1},
	}
	counts := map[string]int{}
	for i := 0; i < 700; i++ {
		selected := b.Select(candidates)
		counts[selected.ProviderName]++
	}
	aPct := float64(counts["a"]) / 700.0
	require.InDelta(t, 5.0/7.0, aPct, 0.05)
}
