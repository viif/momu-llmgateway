package decision

import (
	"strings"

	"github.com/viif/momu-llmgateway/internal/model"
)

type RouteDecision struct {
	ProviderName string
	Model        string
	Strategy     string
}

type RouterConfig struct {
	Strategies     []string
	DefaultCascade []string
}

type ModelProvidersFunc func(model string) []model.Provider
type BuildCandidatesFunc func(providers []model.Provider, model string) []ProviderCandidate

type Router struct {
	strategies       []string
	defaultCascade   []string
	balancer         *Balancer
	semanticRouter   *SemanticRouter
	capabilityRouter *CapabilityRouter
	costRouter       *CostRouter
	modelProviders   ModelProvidersFunc
	buildCandidates  BuildCandidatesFunc
}

func NewRouter(
	cfg RouterConfig,
	balancer *Balancer,
	semantic *SemanticRouter,
	capability *CapabilityRouter,
	cost *CostRouter,
	modelProviders ModelProvidersFunc,
	buildCandidates BuildCandidatesFunc,
) *Router {
	return &Router{
		strategies:       cfg.Strategies,
		defaultCascade:   cfg.DefaultCascade,
		balancer:         balancer,
		semanticRouter:   semantic,
		capabilityRouter: capability,
		costRouter:       cost,
		modelProviders:   modelProviders,
		buildCandidates:  buildCandidates,
	}
}

func (r *Router) Route(req *model.StandardRequest) (RouteDecision, error) {
	if strings.Contains(req.Model, "/") {
		parts := strings.SplitN(req.Model, "/", 2)
		return RouteDecision{ProviderName: parts[0], Model: parts[1], Strategy: "explicit"}, nil
	}

	for _, strategy := range r.strategies {
		switch strategy {
		case "explicit":
			continue

		case "semantic":
			if r.semanticRouter != nil {
				models, _, _ := r.semanticRouter.Route(req)
				if dec, ok := r.resolveModelList(models, "semantic"); ok {
					return dec, nil
				}
			}

		case "capability":
			if r.capabilityRouter != nil {
				tokenEstimate := estimateInputTokens(req.Messages)
				models := r.capabilityRouter.Route(req, tokenEstimate)
				if dec, ok := r.resolveModelList(models, "capability"); ok {
					return dec, nil
				}
			}

		case "cost_cascade":
			if r.costRouter != nil {
				chain := r.costRouter.CascadeFor(req.Model)
				if len(chain) == 0 {
					chain = r.defaultCascade
				}
				if dec, ok := r.resolveModelList(chain, "cost_cascade"); ok {
					return dec, nil
				}
			}
		}
	}

	if len(r.defaultCascade) > 0 {
		if dec, ok := r.resolveModelList(r.defaultCascade, "default"); ok {
			return dec, nil
		}
	}

	if req.Model != "" {
		providers := r.modelProviders(req.Model)
		if dec, ok := r.resolveWithBalancer(providers, req.Model, "default"); ok {
			return dec, nil
		}
	}

	return RouteDecision{}, model.NewError(model.ErrCodeModelNotFound, "no route matched")
}

func (r *Router) resolveModelList(models []string, strategy string) (RouteDecision, bool) {
	for _, m := range models {
		providers := r.modelProviders(m)
		if len(providers) == 0 {
			continue
		}
		if dec, ok := r.resolveWithBalancer(providers, m, strategy); ok {
			return dec, true
		}
	}
	return RouteDecision{}, false
}

func (r *Router) resolveWithBalancer(providers []model.Provider, model, strategy string) (RouteDecision, bool) {
	if r.balancer != nil && r.buildCandidates != nil {
		candidates := r.buildCandidates(providers, model)
		if len(candidates) > 0 {
			selected := r.balancer.Select(candidates)
			if selected.ProviderName != "" {
				return RouteDecision{ProviderName: selected.ProviderName, Model: model, Strategy: strategy}, true
			}
		}
	}
	if len(providers) > 0 {
		return RouteDecision{ProviderName: providers[0].Name(), Model: model, Strategy: strategy}, true
	}
	return RouteDecision{}, false
}

func estimateInputTokens(messages []model.Message) int {
	totalChars := 0
	for _, m := range messages {
		totalChars += len(m.Content)
	}
	if totalChars > 0 {
		return totalChars / 4
	}
	return 0
}
