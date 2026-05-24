package decision

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viif/momu-llmgateway/internal/config"
	"github.com/viif/momu-llmgateway/internal/model"
)

func TestCapabilityMatchByTaskType(t *testing.T) {
	cr := NewCapabilityRouter([]config.RoutingRuleConfig{
		{TaskType: "long_context", TargetModels: []string{"claude-sonnet-4-20250514"}},
	})
	models := cr.Route(&model.StandardRequest{TaskType: "long_context"}, 0)
	require.Equal(t, []string{"claude-sonnet-4-20250514"}, models)
}

func TestCapabilityMismatchTaskType(t *testing.T) {
	cr := NewCapabilityRouter([]config.RoutingRuleConfig{
		{TaskType: "long_context", TargetModels: []string{"claude-sonnet-4-20250514"}},
	})
	models := cr.Route(&model.StandardRequest{TaskType: "code"}, 0)
	require.Nil(t, models)
}

func TestCapabilityConditionGreaterThan(t *testing.T) {
	cr := NewCapabilityRouter([]config.RoutingRuleConfig{
		{TaskType: "long_context", Condition: "input_tokens > 100000", TargetModels: []string{"claude-sonnet-4-20250514"}},
	})
	models := cr.Route(&model.StandardRequest{TaskType: "long_context"}, 150000)
	require.Equal(t, []string{"claude-sonnet-4-20250514"}, models)
}

func TestCapabilityConditionLessThan(t *testing.T) {
	cr := NewCapabilityRouter([]config.RoutingRuleConfig{
		{TaskType: "long_context", Condition: "input_tokens > 100000", TargetModels: []string{"claude-sonnet-4-20250514"}},
	})
	models := cr.Route(&model.StandardRequest{TaskType: "long_context"}, 50000)
	require.Nil(t, models)
}

func TestCapabilityInvalidConditionIgnored(t *testing.T) {
	cr := NewCapabilityRouter([]config.RoutingRuleConfig{
		{TaskType: "test", Condition: "unknown_field > 10", TargetModels: []string{"m"}},
	})
	models := cr.Route(&model.StandardRequest{TaskType: "test"}, 0)
	require.Nil(t, models)
}

func TestCapabilityMultipleRulesFirstWins(t *testing.T) {
	cr := NewCapabilityRouter([]config.RoutingRuleConfig{
		{TaskType: "a", TargetModels: []string{"model-a"}},
		{TaskType: "a", TargetModels: []string{"model-b"}},
	})
	models := cr.Route(&model.StandardRequest{TaskType: "a"}, 0)
	require.Equal(t, []string{"model-a"}, models)
}

func TestCapabilityNoRules(t *testing.T) {
	cr := NewCapabilityRouter(nil)
	models := cr.Route(&model.StandardRequest{TaskType: "foo"}, 0)
	require.Nil(t, models)
}

func TestCapabilityConditionGreaterOrEqual(t *testing.T) {
	cr := NewCapabilityRouter([]config.RoutingRuleConfig{
		{TaskType: "t", Condition: "input_tokens >= 100", TargetModels: []string{"m"}},
	})
	models := cr.Route(&model.StandardRequest{TaskType: "t"}, 100)
	require.Equal(t, []string{"m"}, models)
}

func TestCapabilityConditionLessOrEqual(t *testing.T) {
	cr := NewCapabilityRouter([]config.RoutingRuleConfig{
		{TaskType: "t", Condition: "input_tokens <= 50", TargetModels: []string{"m"}},
	})
	models := cr.Route(&model.StandardRequest{TaskType: "t"}, 50)
	require.Equal(t, []string{"m"}, models)
}
