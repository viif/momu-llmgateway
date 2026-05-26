package decision

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viif/momu-llmgateway/internal/config"
	"github.com/viif/momu-llmgateway/internal/model"
)

type fakeEmbedder struct {
	vectors map[string][]float64
}

func (f *fakeEmbedder) Embed(texts []string) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i, t := range texts {
		if v, ok := f.vectors[t]; ok {
			out[i] = v
		} else {
			out[i] = make([]float64, 2)
		}
	}
	return out, nil
}

func TestSemanticRouterPrecomputesPrototypes(t *testing.T) {
	fake := &fakeEmbedder{
		vectors: map[string][]float64{
			"code1": {1.0, 0.0},
			"code2": {0.9, 0.1},
		},
	}
	cfg := config.SemanticRoutingConfig{
		SimilarityThreshold: 0.75,
		Categories: []config.SemanticCategoryConfig{
			{Name: "code", TargetModels: []string{"deepseek-chat"}, Exemplars: []string{"code1", "code2"}},
		},
	}
	sr, err := NewSemanticRouter(cfg, fake)
	require.NoError(t, err)
	require.Len(t, sr.categories, 1)
	require.Equal(t, "code", sr.categories[0].Name)
	require.Len(t, sr.categories[0].Vector, 2)
}

func TestSemanticRouteMatchAboveThreshold(t *testing.T) {
	fake := &fakeEmbedder{
		vectors: map[string][]float64{
			"code1":      {1.0, 0.0},
			"code2":      {0.9, -0.1},
			"user query": {0.8, 0.1},
		},
	}
	cfg := config.SemanticRoutingConfig{
		SimilarityThreshold: 0.75,
		Categories: []config.SemanticCategoryConfig{
			{Name: "code", TargetModels: []string{"deepseek-chat"}, Exemplars: []string{"code1", "code2"}},
			{Name: "creative", TargetModels: []string{"claude-sonnet-4-20250514"}, Exemplars: []string{"write a poem"}},
		},
	}
	sr, err := NewSemanticRouter(cfg, fake)
	require.NoError(t, err)

	models, category, score := sr.Route(&model.StandardRequest{
		Messages: []model.Message{{Role: "user", Content: "user query"}},
	})
	require.NotNil(t, models)
	require.Equal(t, "code", category)
	require.GreaterOrEqual(t, score, 0.75)
	require.Equal(t, []string{"deepseek-chat"}, models)
}

func TestSemanticRouteMissBelowThreshold(t *testing.T) {
	fake := &fakeEmbedder{
		vectors: map[string][]float64{
			"code1":      {1.0, 0.0},
			"code2":      {0.9, 0.1},
			"user query": {0.0, 1.0},
		},
	}
	cfg := config.SemanticRoutingConfig{
		SimilarityThreshold: 0.75,
		Categories: []config.SemanticCategoryConfig{
			{Name: "code", TargetModels: []string{"deepseek-chat"}, Exemplars: []string{"code1", "code2"}},
		},
	}
	sr, err := NewSemanticRouter(cfg, fake)
	require.NoError(t, err)

	models, _, _ := sr.Route(&model.StandardRequest{
		Messages: []model.Message{{Role: "user", Content: "user query"}},
	})
	require.Nil(t, models)
}

func TestSemanticRouteEmptyMessages(t *testing.T) {
	sr := &SemanticRouter{threshold: 0.75}
	models, _, _ := sr.Route(&model.StandardRequest{
		Messages: []model.Message{},
	})
	require.Nil(t, models)
}

func TestSemanticRouteSkipLongPrompt(t *testing.T) {
	fake := &fakeEmbedder{
		vectors: map[string][]float64{
			"code1": {1.0, 0.0},
		},
	}
	cfg := config.SemanticRoutingConfig{
		SimilarityThreshold: 0.75,
		MaxPromptLength:     3,
		Categories: []config.SemanticCategoryConfig{
			{Name: "code", TargetModels: []string{"deepseek-chat"}, Exemplars: []string{"code1"}},
		},
	}
	sr, err := NewSemanticRouter(cfg, fake)
	require.NoError(t, err)

	models, _, _ := sr.Route(&model.StandardRequest{
		Messages: []model.Message{{Role: "user", Content: "hello world"}},
	})
	require.Nil(t, models)
}

func TestSemanticRoutePromptWithinLimit(t *testing.T) {
	fake := &fakeEmbedder{
		vectors: map[string][]float64{
			"code1": {1.0, 0.0},
			"hi":    {0.9, 0.1},
		},
	}
	cfg := config.SemanticRoutingConfig{
		SimilarityThreshold: 0.75,
		MaxPromptLength:     5,
		Categories: []config.SemanticCategoryConfig{
			{Name: "code", TargetModels: []string{"deepseek-chat"}, Exemplars: []string{"code1"}},
		},
	}
	sr, err := NewSemanticRouter(cfg, fake)
	require.NoError(t, err)

	models, _, _ := sr.Route(&model.StandardRequest{
		Messages: []model.Message{{Role: "user", Content: "hi"}},
	})
	require.NotNil(t, models)
	require.Equal(t, []string{"deepseek-chat"}, models)
}

func TestSemanticRouteMaxPromptLengthZero(t *testing.T) {
	fake := &fakeEmbedder{
		vectors: map[string][]float64{
			"code1": {1.0, 0.0},
			"hi":    {0.9, 0.1},
		},
	}
	cfg := config.SemanticRoutingConfig{
		SimilarityThreshold: 0.75,
		MaxPromptLength:     0,
		Categories: []config.SemanticCategoryConfig{
			{Name: "code", TargetModels: []string{"deepseek-chat"}, Exemplars: []string{"code1"}},
		},
	}
	sr, err := NewSemanticRouter(cfg, fake)
	require.NoError(t, err)

	models, _, _ := sr.Route(&model.StandardRequest{
		Messages: []model.Message{{Role: "user", Content: "hi"}},
	})
	require.NotNil(t, models)
	require.Equal(t, []string{"deepseek-chat"}, models)
}

func TestSemanticRouteNoEngine(t *testing.T) {
	cfg := config.SemanticRoutingConfig{
		SimilarityThreshold: 0.75,
		Categories: []config.SemanticCategoryConfig{
			{Name: "code", TargetModels: []string{"deepseek-chat"}, Exemplars: []string{"code1"}},
		},
	}
	sr, err := NewSemanticRouter(cfg, nil)
	require.NoError(t, err)
	models, _, _ := sr.Route(&model.StandardRequest{
		Messages: []model.Message{{Role: "user", Content: "query"}},
	})
	require.Nil(t, models)
}
