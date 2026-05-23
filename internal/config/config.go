package config

import (
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

var currentConfig atomic.Value // stores *Config

type Config struct {
	Server          ServerConfig              `mapstructure:"server"`
	Redis           RedisConfig               `mapstructure:"redis"`
	Auth            AuthConfig                `mapstructure:"auth"`
	Providers       map[string]ProviderConfig `mapstructure:"providers"`
	Routing         RoutingConfig             `mapstructure:"routing"`
	SemanticRouting SemanticRoutingConfig     `mapstructure:"semantic_routing"`
	SemanticCache   SemanticCacheConfig       `mapstructure:"semantic_cache"`
	Fallback        FallbackConfig            `mapstructure:"fallback"`
	CircuitBreaker  CircuitBreakerConfig      `mapstructure:"circuit_breaker"`
}

type ServerConfig struct {
	Port         int           `mapstructure:"port"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout"`
}

type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

type AuthConfig struct {
	APIKeys []APIKeyConfig `mapstructure:"api_keys"`
}

type APIKeyConfig struct {
	Key           string   `mapstructure:"key"`
	Name          string   `mapstructure:"name"`
	RateLimit     int      `mapstructure:"rate_limit"`
	AllowedModels []string `mapstructure:"allowed_models"`
}

type ProviderConfig struct {
	Type    string        `mapstructure:"type"`
	BaseURL string        `mapstructure:"base_url"`
	APIKey  string        `mapstructure:"api_key"`
	Models  []string      `mapstructure:"models"`
	Weight  int           `mapstructure:"weight"`
	Timeout time.Duration `mapstructure:"timeout"`
}

type RoutingConfig struct {
	Strategies []string            `mapstructure:"strategies"`
	Rules      []RoutingRuleConfig `mapstructure:"rules"`
	Cascade    map[string][]string `mapstructure:"cascade"`
}

type RoutingRuleConfig struct {
	TaskType     string   `mapstructure:"task_type"`
	Condition    string   `mapstructure:"condition"`
	TargetModels []string `mapstructure:"target_models"`
}

type SemanticRoutingConfig struct {
	EmbeddingProvider   string                   `mapstructure:"embedding_provider"`
	EmbeddingModel      string                   `mapstructure:"embedding_model"`
	SimilarityThreshold float64                  `mapstructure:"similarity_threshold"`
	Categories          []SemanticCategoryConfig `mapstructure:"categories"`
}

type SemanticCategoryConfig struct {
	Name         string   `mapstructure:"name"`
	TargetModels []string `mapstructure:"target_models"`
	Exemplars    []string `mapstructure:"exemplars"`
}

type SemanticCacheConfig struct {
	Enabled             bool          `mapstructure:"enabled"`
	SimilarityThreshold float64       `mapstructure:"similarity_threshold"`
	TTL                 time.Duration `mapstructure:"ttl"`
	MaxEntries          int           `mapstructure:"max_entries"`
	EmbeddingProvider   string        `mapstructure:"embedding_provider"`
	EmbeddingModel      string        `mapstructure:"embedding_model"`
}

type FallbackConfig struct {
	RetryMax     int                 `mapstructure:"retry_max"`
	RetryBackoff time.Duration       `mapstructure:"retry_backoff"`
	Chains       map[string][]string `mapstructure:"chains"`
}

type CircuitBreakerConfig struct {
	FailureThreshold int           `mapstructure:"failure_threshold"`
	Window           time.Duration `mapstructure:"window"`
	Cooldown         time.Duration `mapstructure:"cooldown"`
}

func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}
	for _, key := range v.AllKeys() {
		if s, ok := v.Get(key).(string); ok {
			v.Set(key, os.ExpandEnv(s))
		}
	}
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}
	currentConfig.Store(&cfg)
	return &cfg, nil
}

func GetConfig() *Config {
	if v := currentConfig.Load(); v != nil {
		return v.(*Config)
	}
	return nil
}

func WatchAndReload(path string, onChange func(*Config)) error {
	v := viper.New()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return err
	}
	v.WatchConfig()
	v.OnConfigChange(func(_ fsnotify.Event) {
		cfg, err := Load(path)
		if err == nil && onChange != nil {
			onChange(cfg)
		}
	})
	return nil
}
