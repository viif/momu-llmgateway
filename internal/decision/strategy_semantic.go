package decision

import (
	"github.com/viif/momu-llmgateway/internal/config"
	"github.com/viif/momu-llmgateway/internal/embedding"
	"github.com/viif/momu-llmgateway/internal/model"
)

type Embedder interface {
	Embed(texts []string) ([][]float64, error)
}

type CategoryPrototype struct {
	Name         string
	TargetModels []string
	Vector       []float64
}

type SemanticRouter struct {
	categories      []CategoryPrototype
	threshold       float64
	maxPromptLength int
	engine          Embedder
}

func NewSemanticRouter(cfg config.SemanticRoutingConfig, eng Embedder) (*SemanticRouter, error) {
	sr := &SemanticRouter{threshold: cfg.SimilarityThreshold, maxPromptLength: cfg.MaxPromptLength, engine: eng}
	if eng == nil || len(cfg.Categories) == 0 {
		return sr, nil
	}

	for _, cat := range cfg.Categories {
		vecs, err := eng.Embed(cat.Exemplars)
		if err != nil {
			return nil, err
		}
		if len(vecs) == 0 {
			continue
		}

		prototype := make([]float64, len(vecs[0]))
		for _, v := range vecs {
			for i := range v {
				prototype[i] += v[i]
			}
		}
		n := float64(len(vecs))
		for i := range prototype {
			prototype[i] /= n
		}
		prototype = embedding.NormalizeVector(prototype)

		sr.categories = append(sr.categories, CategoryPrototype{
			Name:         cat.Name,
			TargetModels: append([]string(nil), cat.TargetModels...),
			Vector:       prototype,
		})
	}
	return sr, nil
}

func (sr *SemanticRouter) Route(req *model.StandardRequest) (models []string, category string, confidence float64) {
	if sr.engine == nil || len(sr.categories) == 0 {
		return nil, "", 0
	}

	text := concatenateUserMessages(req.Messages)
	if text == "" {
		return nil, "", 0
	}

	if sr.maxPromptLength > 0 && len([]rune(text)) > sr.maxPromptLength {
		return nil, "", 0
	}

	vecs, err := sr.engine.Embed([]string{text})
	if err != nil || len(vecs) == 0 {
		return nil, "", 0
	}

	for _, cat := range sr.categories {
		score := embedding.CosineSimilarity(vecs[0], cat.Vector)
		if score >= sr.threshold && score > confidence {
			confidence = score
			category = cat.Name
			models = cat.TargetModels
		}
	}
	return models, category, confidence
}

func concatenateUserMessages(messages []model.Message) string {
	var parts []string
	for _, m := range messages {
		if m.Role == "user" && m.Content != "" {
			parts = append(parts, m.Content)
		}
	}
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}
