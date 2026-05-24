package decision

import (
	"strconv"
	"strings"

	"github.com/viif/momu-llmgateway/internal/config"
	"github.com/viif/momu-llmgateway/internal/model"
)

type CapabilityRouter struct {
	rules []config.RoutingRuleConfig
}

func NewCapabilityRouter(rules []config.RoutingRuleConfig) *CapabilityRouter {
	return &CapabilityRouter{rules: rules}
}

func (cr *CapabilityRouter) Route(req *model.StandardRequest, estimatedTokens int) []string {
	for _, rule := range cr.rules {
		if matchesRule(rule, req, estimatedTokens) {
			return rule.TargetModels
		}
	}
	return nil
}

func matchesRule(rule config.RoutingRuleConfig, req *model.StandardRequest, estimatedTokens int) bool {
	if rule.TaskType != "" && rule.TaskType != req.TaskType {
		return false
	}
	if rule.Condition != "" && !evaluateCondition(rule.Condition, estimatedTokens) {
		return false
	}
	return true
}

func evaluateCondition(condition string, inputTokens int) bool {
	parts := strings.Fields(condition)
	if len(parts) != 3 || parts[0] != "input_tokens" {
		return false
	}
	threshold, err := strconv.Atoi(parts[2])
	if err != nil {
		return false
	}
	switch parts[1] {
	case ">":
		return inputTokens > threshold
	case "<":
		return inputTokens < threshold
	case ">=":
		return inputTokens >= threshold
	case "<=":
		return inputTokens <= threshold
	default:
		return false
	}
}
