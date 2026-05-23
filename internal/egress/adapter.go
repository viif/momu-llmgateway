package egress

import (
	"sync"

	"github.com/viif/momu-llmgateway/internal/model"
)

type Registry struct {
	mu        sync.RWMutex
	providers []model.Provider
	byName    map[string]model.Provider
	byModel   map[string][]model.Provider
}

func NewRegistry() *Registry {
	return &Registry{
		byName:  make(map[string]model.Provider),
		byModel: make(map[string][]model.Provider),
	}
}

func (r *Registry) Register(p model.Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers = append(r.providers, p)
	r.byName[p.Name()] = p
	for _, m := range p.Models() {
		r.byModel[m] = append(r.byModel[m], p)
	}
}

func (r *Registry) Providers() []model.Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]model.Provider, len(r.providers))
	copy(out, r.providers)
	return out
}

func (r *Registry) ProvidersForModel(modelID string) []model.Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	providers := r.byModel[modelID]
	out := make([]model.Provider, len(providers))
	copy(out, providers)
	return out
}

func (r *Registry) ProviderByName(name string) model.Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byName[name]
}
