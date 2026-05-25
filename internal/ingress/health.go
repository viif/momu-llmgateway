package ingress

import (
	"context"
	"sync"

	"github.com/viif/momu-llmgateway/internal/cache"
	"github.com/viif/momu-llmgateway/internal/egress"
)

type HealthResult struct {
	Status string               `json:"status"`
	Checks map[string]CheckItem `json:"checks"`
}

type CheckItem struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type HealthChecker struct {
	store    cache.CacheStore
	registry *egress.Registry
}

func NewHealthChecker(store cache.CacheStore, registry *egress.Registry) *HealthChecker {
	return &HealthChecker{store: store, registry: registry}
}

func (h *HealthChecker) Check(ctx context.Context) *HealthResult {
	result := &HealthResult{
		Status: "ok",
		Checks: make(map[string]CheckItem),
	}

	var mu sync.Mutex
	var anyFailed bool

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		if h.store == nil {
			mu.Lock()
			result.Checks["redis"] = CheckItem{Status: "not_configured"}
			mu.Unlock()
			return
		}
		if err := h.store.Ping(ctx); err != nil {
			mu.Lock()
			result.Checks["redis"] = CheckItem{Status: "error", Error: err.Error()}
			anyFailed = true
			mu.Unlock()
		} else {
			mu.Lock()
			result.Checks["redis"] = CheckItem{Status: "ok"}
			mu.Unlock()
		}
	}()

	if h.registry != nil {
		for _, p := range h.registry.Providers() {
			wg.Add(1)
			go func(providerName string, checkFn func(context.Context) error) {
				defer wg.Done()
				if err := checkFn(ctx); err != nil {
					mu.Lock()
					result.Checks[providerName] = CheckItem{Status: "error", Error: err.Error()}
					anyFailed = true
					mu.Unlock()
				} else {
					mu.Lock()
					result.Checks[providerName] = CheckItem{Status: "ok"}
					mu.Unlock()
				}
			}(p.Name(), p.HealthCheck)
		}
	}

	wg.Wait()

	if anyFailed {
		result.Status = "degraded"
	}

	return result
}
