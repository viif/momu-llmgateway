package decision

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viif/momu-llmgateway/internal/config"
	"github.com/viif/momu-llmgateway/internal/model"
)

type fakeRegProvider struct {
	name   string
	models []string
}

func (f fakeRegProvider) Name() string     { return f.name }
func (f fakeRegProvider) Models() []string { return f.models }
func (f fakeRegProvider) Send(context.Context, *model.StandardRequest) (*model.StandardResponse, error) {
	return nil, nil
}
func (f fakeRegProvider) SendStream(context.Context, *model.StandardRequest) (<-chan model.StreamChunk, error) {
	return nil, nil
}

func TestRouterExplicitRoute(t *testing.T) {
	r := NewRouter(RouterConfig{Strategies: []string{"semantic"}}, nil, nil, nil, nil, nil, nil)
	dec, err := r.Route(&model.StandardRequest{Model: "openai/gpt-4o"})
	require.NoError(t, err)
	require.Equal(t, "openai", dec.ProviderName)
	require.Equal(t, "gpt-4o", dec.Model)
	require.Equal(t, "explicit", dec.Strategy)
}

func TestRouterModelNotFound(t *testing.T) {
	r := NewRouter(RouterConfig{Strategies: []string{}}, nil, nil, nil, nil, nil, nil)
	_, err := r.Route(&model.StandardRequest{Model: ""})
	require.Error(t, err)
}

func TestRouterWithBalancerSelectsProvider(t *testing.T) {
	b := NewBalancer(BalancerConfig{})
	b.Register("openai")
	b.Register("deepseek")

	modelProviders := func(modelName string) []model.Provider {
		if modelName == "gpt-4o" {
			return []model.Provider{
				fakeRegProvider{name: "openai", models: []string{"gpt-4o"}},
				fakeRegProvider{name: "deepseek", models: []string{"gpt-4o"}},
			}
		}
		return nil
	}
	buildCandidates := func(providers []model.Provider, modelName string) []ProviderCandidate {
		cands := make([]ProviderCandidate, len(providers))
		for i, p := range providers {
			w := 100.0
			if p.Name() == "openai" {
				w = 1
			}
			cands[i] = ProviderCandidate{ProviderName: p.Name(), Model: modelName, BaseWeight: w, HealthScore: 1, WarmupFactor: 1}
		}
		return cands
	}

	r := NewRouter(
		RouterConfig{Strategies: []string{"cost_cascade"}, DefaultCascade: []string{}},
		b, nil, nil, nil, modelProviders, buildCandidates,
	)
	dec, err := r.Route(&model.StandardRequest{Model: "gpt-4o"})
	require.NoError(t, err)
	require.Equal(t, "deepseek", dec.ProviderName)
	require.Equal(t, "gpt-4o", dec.Model)
}

func TestRouterDefaultCascade(t *testing.T) {
	b := NewBalancer(BalancerConfig{})
	b.Register("deepseek")

	modelProviders := func(modelName string) []model.Provider {
		if modelName == "deepseek-chat" {
			return []model.Provider{fakeRegProvider{name: "deepseek", models: []string{"deepseek-chat"}}}
		}
		return nil
	}
	buildCandidates := func(providers []model.Provider, modelName string) []ProviderCandidate {
		cands := make([]ProviderCandidate, len(providers))
		for i, p := range providers {
			cands[i] = ProviderCandidate{ProviderName: p.Name(), Model: modelName, BaseWeight: 100, HealthScore: 1, WarmupFactor: 1}
		}
		return cands
	}

	r := NewRouter(
		RouterConfig{Strategies: []string{}, DefaultCascade: []string{"deepseek-chat"}},
		b, nil, nil, nil, modelProviders, buildCandidates,
	)
	dec, err := r.Route(&model.StandardRequest{Model: ""})
	require.NoError(t, err)
	require.Equal(t, "deepseek-chat", dec.Model)
	require.Equal(t, "deepseek", dec.ProviderName)
	require.Equal(t, "default", dec.Strategy)
}

func TestRouterStrategyOrder(t *testing.T) {
	b := NewBalancer(BalancerConfig{})
	b.Register("deepseek")

	modelProviders := func(modelName string) []model.Provider {
		if modelName == "gpt-4o" {
			return []model.Provider{fakeRegProvider{name: "deepseek", models: []string{"gpt-4o"}}}
		}
		return nil
	}
	buildCandidates := func(providers []model.Provider, modelName string) []ProviderCandidate {
		cands := make([]ProviderCandidate, len(providers))
		for i, p := range providers {
			cands[i] = ProviderCandidate{ProviderName: p.Name(), Model: modelName, BaseWeight: 100, HealthScore: 1, WarmupFactor: 1}
		}
		return cands
	}

	capRouter := NewCapabilityRouter([]config.RoutingRuleConfig{
		{TaskType: "long_context", TargetModels: []string{"gpt-4o"}},
	})

	r := NewRouter(
		RouterConfig{Strategies: []string{"capability"}, DefaultCascade: []string{"deepseek-chat"}},
		b, nil, capRouter, nil, modelProviders, buildCandidates,
	)
	dec, err := r.Route(&model.StandardRequest{Model: "claude-sonnet-4-20250514", TaskType: "long_context"})
	require.NoError(t, err)
	require.Equal(t, "gpt-4o", dec.Model)
	require.Equal(t, "capability", dec.Strategy)
}

func TestRouterCostCascadeFallsBackToDefault(t *testing.T) {
	b := NewBalancer(BalancerConfig{})
	b.Register("deepseek")

	modelProviders := func(modelName string) []model.Provider {
		if modelName == "deepseek-chat" {
			return []model.Provider{fakeRegProvider{name: "deepseek", models: []string{"deepseek-chat"}}}
		}
		return nil
	}
	buildCandidates := func(providers []model.Provider, modelName string) []ProviderCandidate {
		cands := make([]ProviderCandidate, len(providers))
		for i, p := range providers {
			cands[i] = ProviderCandidate{ProviderName: p.Name(), Model: modelName, BaseWeight: 100, HealthScore: 1, WarmupFactor: 1}
		}
		return cands
	}

	costRouter := NewCostRouter(map[string][]string{
		"gpt-4o":  {"gpt-4o-mini"},
		"default": {"deepseek-chat"},
	})

	r := NewRouter(
		RouterConfig{Strategies: []string{"cost_cascade"}, DefaultCascade: []string{"deepseek-chat"}},
		b, nil, nil, costRouter, modelProviders, buildCandidates,
	)

	dec, err := r.Route(&model.StandardRequest{Model: "unknown"})
	require.NoError(t, err)
	require.Equal(t, "deepseek-chat", dec.Model)
}

func TestRouterModelListSkipsUnavailableProviders(t *testing.T) {
	b := NewBalancer(BalancerConfig{})
	b.Register("deepseek")

	modelProviders := func(modelName string) []model.Provider {
		if modelName == "deepseek-chat" {
			return []model.Provider{fakeRegProvider{name: "deepseek", models: []string{"deepseek-chat"}}}
		}
		return nil
	}
	buildCandidates := func(providers []model.Provider, modelName string) []ProviderCandidate {
		cands := make([]ProviderCandidate, len(providers))
		for i, p := range providers {
			cands[i] = ProviderCandidate{ProviderName: p.Name(), Model: modelName, BaseWeight: 100, HealthScore: 1, WarmupFactor: 1}
		}
		return cands
	}

	r := NewRouter(
		RouterConfig{Strategies: []string{}, DefaultCascade: []string{"gpt-4o-mini", "deepseek-chat"}},
		b, nil, nil, nil, modelProviders, buildCandidates,
	)
	dec, err := r.Route(&model.StandardRequest{Model: ""})
	require.NoError(t, err)
	require.Equal(t, "deepseek-chat", dec.Model)
}

func TestRouterSemanticViaIntegration(t *testing.T) {
	b := NewBalancer(BalancerConfig{})
	b.Register("deepseek")

	fake := &fakeEmbedder{
		vectors: map[string][]float64{
			"code1":         {1.0, 0.0},
			"user is query": {0.9, 0.05},
		},
	}

	semanticRouter, err := NewSemanticRouter(
		config.SemanticRoutingConfig{
			SimilarityThreshold: 0.75,
			Categories: []config.SemanticCategoryConfig{
				{Name: "code", TargetModels: []string{"deepseek-chat"}, Exemplars: []string{"code1"}},
			},
		},
		fake,
	)
	require.NoError(t, err)

	modelProviders := func(modelName string) []model.Provider {
		if modelName == "deepseek-chat" {
			return []model.Provider{fakeRegProvider{name: "deepseek", models: []string{"deepseek-chat"}}}
		}
		return nil
	}
	buildCandidates := func(providers []model.Provider, modelName string) []ProviderCandidate {
		cands := make([]ProviderCandidate, len(providers))
		for i, p := range providers {
			cands[i] = ProviderCandidate{ProviderName: p.Name(), Model: modelName, BaseWeight: 100, HealthScore: 1, WarmupFactor: 1}
		}
		return cands
	}

	r := NewRouter(
		RouterConfig{Strategies: []string{"semantic"}, DefaultCascade: []string{"gpt-4o-mini"}},
		b, semanticRouter, nil, nil, modelProviders, buildCandidates,
	)

	dec, err := r.Route(&model.StandardRequest{
		Model:    "gpt-4o",
		Messages: []model.Message{{Role: "user", Content: "user is query"}},
	})
	require.NoError(t, err)
	require.Equal(t, "deepseek-chat", dec.Model)
	require.Equal(t, "semantic", dec.Strategy)
}

func TestRouterExplicitBypassesStrategyChain(t *testing.T) {
	b := NewBalancer(BalancerConfig{})

	capRouter := NewCapabilityRouter([]config.RoutingRuleConfig{
		{TaskType: "code", TargetModels: []string{"gpt-4o"}},
	})

	modelProviders := func(modelName string) []model.Provider {
		if modelName == "gpt-4o" {
			return []model.Provider{fakeRegProvider{name: "openai", models: []string{"gpt-4o"}}}
		}
		return nil
	}
	buildCandidates := func(providers []model.Provider, modelName string) []ProviderCandidate {
		return []ProviderCandidate{{ProviderName: "openai", Model: modelName, BaseWeight: 1, HealthScore: 1, WarmupFactor: 1}}
	}

	r := NewRouter(
		RouterConfig{Strategies: []string{"capability"}, DefaultCascade: []string{}},
		b, nil, capRouter, nil, modelProviders, buildCandidates,
	)

	dec, err := r.Route(&model.StandardRequest{Model: "deepseek/gpt-4o", TaskType: "code"})
	require.NoError(t, err)
	require.Equal(t, "deepseek", dec.ProviderName)
	require.Equal(t, "gpt-4o", dec.Model)
	require.Equal(t, "explicit", dec.Strategy)
}
