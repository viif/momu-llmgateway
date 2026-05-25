package egress

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viif/momu-llmgateway/internal/model"
)

type fakeProvider struct {
	name   string
	models []string
}

func (f fakeProvider) Name() string     { return f.name }
func (f fakeProvider) Models() []string { return f.models }
func (f fakeProvider) Send(context.Context, *model.StandardRequest) (*model.StandardResponse, error) {
	return nil, nil
}
func (f fakeProvider) SendStream(context.Context, *model.StandardRequest) (<-chan model.StreamChunk, error) {
	return nil, nil
}
func (f fakeProvider) HealthCheck(ctx context.Context) error { return nil }

func TestRegistryFindsProvidersByModel(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeProvider{name: "openai", models: []string{"gpt-4o"}})
	providers := r.ProvidersForModel("gpt-4o")
	require.Len(t, providers, 1)
	require.Equal(t, "openai", providers[0].Name())
}

func TestRegistryProviderByName(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeProvider{name: "openai", models: []string{"gpt-4o"}})
	require.NotNil(t, r.ProviderByName("openai"))
	require.Nil(t, r.ProviderByName("nonexistent"))
}

func TestRegistryProvidersReturnsCopy(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeProvider{name: "openai", models: []string{"gpt-4o"}})
	list := r.Providers()
	require.Len(t, list, 1)
	// 修改返回值不应影响注册表内部状态
	list[0] = nil
	require.NotNil(t, r.Providers()[0])
}

func TestRegistryProvidersForModelNotFound(t *testing.T) {
	r := NewRegistry()
	providers := r.ProvidersForModel("nonexistent")
	require.Empty(t, providers)
}

func TestRegistryMultipleProvidersSameModel(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeProvider{name: "openai", models: []string{"gpt-4o", "gpt-4o-mini"}})
	r.Register(fakeProvider{name: "deepseek", models: []string{"gpt-4o", "deepseek-chat"}})
	providers := r.ProvidersForModel("gpt-4o")
	require.Len(t, providers, 2)
	names := map[string]bool{}
	for _, p := range providers {
		names[p.Name()] = true
	}
	require.True(t, names["openai"])
	require.True(t, names["deepseek"])
}

func TestRegistryConcurrentAccess(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeProvider{name: "openai", models: []string{"gpt-4o"}})
	r.Register(fakeProvider{name: "deepseek", models: []string{"deepseek-chat"}})

	var wg sync.WaitGroup
	// 并发读
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.ProvidersForModel("gpt-4o")
			_ = r.ProviderByName("openai")
			_ = r.Providers()
		}()
	}
	// 并发写（模拟热注册，虽然启动阶段用但保证安全）
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			r.Register(fakeProvider{name: "extra", models: []string{"extra-model"}})
		}
	}()
	wg.Wait()
}
