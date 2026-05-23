package decision

import (
	"math"
	"sync"
)

type BalancerConfig struct {
	ConcurrencyPenaltyCoefficient float64
	LatencyPenaltyCoefficient     float64
	WarmupEnabled                 bool
	WarmupDuration                float64
	HealthWindowSize              float64
	HealthMinRequests             int
}

type ProviderCandidate struct {
	ProviderName         string
	Model                string
	BaseWeight           float64
	ActiveConnections    int
	NormalizedP99Latency float64
	HealthScore          float64
	WarmupFactor         float64
}

type nodeState struct {
	currentWeight float64
}

type Balancer struct {
	cfg        BalancerConfig
	mu         sync.Mutex
	slots      []nodeState
	nameToSlot map[string]int
}

func NewBalancer(cfg BalancerConfig) *Balancer {
	return &Balancer{
		cfg:        cfg,
		nameToSlot: make(map[string]int),
	}
}

func (b *Balancer) Register(name string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if slot, ok := b.nameToSlot[name]; ok {
		return slot
	}
	b.slots = append(b.slots, nodeState{})
	slot := len(b.slots) - 1
	b.nameToSlot[name] = slot
	return slot
}

func (b *Balancer) EffectiveWeight(c ProviderCandidate) float64 {
	warmup := c.WarmupFactor
	if warmup <= 0 {
		warmup = 0
	}
	base := c.BaseWeight * warmup
	denom := 1 + b.cfg.ConcurrencyPenaltyCoefficient*float64(c.ActiveConnections) + b.cfg.LatencyPenaltyCoefficient*c.NormalizedP99Latency
	loadFactor := 1.0 / denom
	health := c.HealthScore
	if health <= 0 {
		health = 0
	}
	return math.Max(base*loadFactor*health, 0)
}

func (b *Balancer) resolveSlots(candidates []ProviderCandidate) []int {
	slots := make([]int, len(candidates))
	for i, c := range candidates {
		if s, ok := b.nameToSlot[c.ProviderName]; ok {
			slots[i] = s
		} else {
			b.slots = append(b.slots, nodeState{})
			s := len(b.slots) - 1
			b.nameToSlot[c.ProviderName] = s
			slots[i] = s
		}
	}
	return slots
}

func (b *Balancer) Select(candidates []ProviderCandidate) ProviderCandidate {
	if len(candidates) == 0 {
		return ProviderCandidate{}
	}

	n := len(candidates)
	effs := make([]float64, n)
	totalEff := 0.0
	for i := range candidates {
		eff := b.EffectiveWeight(candidates[i])
		effs[i] = eff
		totalEff += eff
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	slots := b.resolveSlots(candidates)

	var bestIdx int
	var bestWeight float64 = -1 << 63
	for i := range candidates {
		b.slots[slots[i]].currentWeight += effs[i]
		if cw := b.slots[slots[i]].currentWeight; cw > bestWeight {
			bestWeight = cw
			bestIdx = i
		}
	}

	b.slots[slots[bestIdx]].currentWeight -= totalEff

	return candidates[bestIdx]
}
