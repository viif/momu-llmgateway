package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/viif/momu-llmgateway/internal/cache"
	"github.com/viif/momu-llmgateway/internal/config"
	"github.com/viif/momu-llmgateway/internal/decision"
	"github.com/viif/momu-llmgateway/internal/egress"
	"github.com/viif/momu-llmgateway/internal/embedding"
	"github.com/viif/momu-llmgateway/internal/fallback"
	"github.com/viif/momu-llmgateway/internal/ingress"
	"github.com/viif/momu-llmgateway/internal/model"
	"github.com/viif/momu-llmgateway/internal/observability"
)

func main() {
	// ── 1. 日志 ──────────────────────────────────────────────
	if err := observability.InitLogger(false); err != nil {
		panic(err)
	}
	log := observability.Logger

	// ── 2. 配置 ──────────────────────────────────────────────
	cfgPath := os.Getenv("GATEWAY_CONFIG")
	if cfgPath == "" {
		cfgPath = "configs/gateway.yaml"
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatal("load config", zap.Error(err))
	}
	_ = config.WatchAndReload(cfgPath, func(*config.Config) {
		log.Info("config reloaded")
	})

	// ── 3. 嵌入引擎 ──────────────────────────────────────────
	if err := embedding.Init(cfg.Embedding.OnnxLibraryPath, cfg.Embedding.ModelPath); err != nil {
		log.Warn("embedding engine init failed, semantic features disabled", zap.Error(err))
	}

	// ── 4. Redis ─────────────────────────────────────────────
	redisClient := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		log.Fatal("redis connect", zap.Error(err))
	}

	// ── 5. Provider 注册表 ──────────────────────────────────
	registry := egress.NewRegistry()
	for name, pc := range cfg.Providers {
		var p model.Provider
		switch pc.Type {
		case "anthropic":
			p = egress.NewAnthropic(pc.BaseURL, pc.APIKey, pc.Models, pc.Timeout)
		default:
			p = egress.NewOpenAICompatible(name, pc.BaseURL, pc.APIKey, pc.Models, pc.Timeout)
		}
		registry.Register(p)
		log.Info("provider registered", zap.String("name", name), zap.Strings("models", pc.Models))
	}

	// ── 6. 负载均衡器 ────────────────────────────────────────
	balancerCfg := decision.BalancerConfig{
		ConcurrencyPenaltyCoefficient: cfg.Balancer.ConcurrencyPenaltyCoefficient,
		LatencyPenaltyCoefficient:     cfg.Balancer.LatencyPenaltyCoefficient,
		WarmupEnabled:                 cfg.Balancer.WarmupEnabled,
		WarmupDuration:                cfg.Balancer.WarmupDuration.Seconds(),
		HealthWindowSize:              cfg.Balancer.HealthWindowSize.Seconds(),
		HealthMinRequests:             cfg.Balancer.HealthMinRequests,
	}
	balancer := decision.NewBalancer(balancerCfg)
	for name := range cfg.Providers {
		balancer.Register(name)
	}

	// ── 7. 熔断器管理器 ──────────────────────────────────────
	cbManager := ingress.NewCircuitBreakerManager(
		cfg.CircuitBreaker.FailureThreshold,
		cfg.CircuitBreaker.Cooldown,
	)

	// ── 8. 路由策略 ──────────────────────────────────────────
	var semanticRouter *decision.SemanticRouter
	capabilityRouter := decision.NewCapabilityRouter(cfg.Routing.Rules)
	costRouter := decision.NewCostRouter(cfg.Routing.Cascade)

	if emb := embedding.Instance(); emb != nil {
		semanticRouter, err = decision.NewSemanticRouter(cfg.SemanticRouting, emb)
		if err != nil {
			log.Warn("semantic router init failed", zap.Error(err))
		} else {
			log.Info("semantic router initialized",
				zap.Int("categories", len(cfg.SemanticRouting.Categories)))
		}
	}

	// ── 9. 语义缓存 ──────────────────────────────────────────
	var (
		semanticCache *cache.SemanticCache
		cacheStore    cache.CacheStore
	)
	if cfg.SemanticCache.Enabled {
		cacheStore, err = cache.NewRedisStore(cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB)
		if err != nil {
			log.Warn("cache redis store init failed, using memory-only", zap.Error(err))
			cacheStore = nil
		}

		cacheCfg := cache.SemanticCacheConfig{
			Enabled:             cfg.SemanticCache.Enabled,
			SimilarityThreshold: cfg.SemanticCache.SimilarityThreshold,
			MaxEntries:          cfg.SemanticCache.MaxEntries,
			TTL:                 cfg.SemanticCache.TTL,
			MaxPromptLength:     cfg.SemanticCache.MaxPromptLength,
		}
		semanticCache = cache.New(cacheCfg, embedding.Instance(), cacheStore)

		if cacheStore != nil {
			allModels := collectAllModels(cfg.Providers)
			semanticCache.SetModels(allModels)
		}
	}

	// ── 10. Fallback 引擎 ────────────────────────────────────
	fallbackEng := fallback.NewEngine(
		cfg.Fallback.Chains,
		cfg.Fallback.RetryMax,
		cfg.Fallback.RetryBackoff,
		"",
	)

	// ── 11. 主 Router ────────────────────────────────────────
	modelProviders := func(modelName string) []model.Provider {
		return registry.ProvidersForModel(modelName)
	}

	buildCandidates := func(providers []model.Provider, modelName string) []decision.ProviderCandidate {
		candidates := make([]decision.ProviderCandidate, len(providers))
		for i, p := range providers {
			pc, ok := cfg.Providers[p.Name()]
			baseWeight := 100.0
			if ok {
				baseWeight = float64(pc.Weight)
			}
			candidates[i] = decision.ProviderCandidate{
				ProviderName: p.Name(),
				Model:        modelName,
				BaseWeight:   baseWeight,
				HealthScore:  1.0,
				WarmupFactor: 1.0,
			}
		}
		return candidates
	}

	router := decision.NewRouter(
		decision.RouterConfig{
			Strategies:     cfg.Routing.Strategies,
			DefaultCascade: cfg.Routing.Cascade["default"],
		},
		balancer,
		semanticRouter,
		capabilityRouter,
		costRouter,
		modelProviders,
		buildCandidates,
	)

	// ── 12. ChatService ──────────────────────────────────────
	providerLookup := func(name string) model.Provider {
		return registry.ProviderByName(name)
	}

	var ingressCache ingress.SemanticCache
	if semanticCache != nil {
		ingressCache = semanticCache
	}

	chatSvc := ingress.NewChatService(
		router,
		cbManager,
		ingressCache,
		fallbackEng,
		providerLookup,
	)

	// ── 13. Prometheus 指标 ──────────────────────────────────
	prometheus.MustRegister(
		observability.RequestDuration,
		observability.RequestTotal,
		observability.TokensTotal,
		observability.FallbackTotal,
		observability.CircuitBreakerState,
		observability.CacheHitTotal,
	)

	// ── 14. HTTP 服务 ────────────────────────────────────────
	allModels := collectAllModels(cfg.Providers)

	r := gin.New()
	r.Use(
		gin.Recovery(),
		ingress.RequestIDMiddleware(),
		ingress.LoggingMiddleware(),
		ingress.AuthMiddleware(cfg.Auth.APIKeys),
		ingress.RateLimitMiddleware(redisClient),
		ingress.ValidationMiddleware(allModels),
	)
	ingress.RegisterRoutes(r, chatSvc)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      r,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	go func() {
		log.Info("gateway starting", zap.Int("port", cfg.Server.Port))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("server failed", zap.Error(err))
		}
	}()

	// ── 15. 优雅关闭 ─────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("server shutdown error", zap.Error(err))
	}

	if redisClient != nil {
		_ = redisClient.Close()
	}
	if emb := embedding.Instance(); emb != nil {
		emb.Close()
	}
	if cacheStore != nil {
		_ = cacheStore.Close()
	}

	log.Info("gateway stopped")
}

func collectAllModels(providers map[string]config.ProviderConfig) []string {
	seen := map[string]bool{}
	var models []string
	for _, p := range providers {
		for _, m := range p.Models {
			if !seen[m] {
				seen[m] = true
				models = append(models, m)
			}
		}
	}
	return models
}
