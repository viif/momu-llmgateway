package decision

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCostCascadeForKnownModel(t *testing.T) {
	cr := NewCostRouter(map[string][]string{
		"gpt-4o":  {"gpt-4o-mini", "deepseek-chat"},
		"default": {"deepseek-chat", "gpt-4o-mini"},
	})
	chain := cr.CascadeFor("gpt-4o")
	require.Equal(t, []string{"gpt-4o-mini", "deepseek-chat"}, chain)
}

func TestCostCascadeFallsBackToDefault(t *testing.T) {
	cr := NewCostRouter(map[string][]string{
		"default": {"deepseek-chat", "gpt-4o-mini"},
	})
	chain := cr.CascadeFor("unknown-model")
	require.Equal(t, []string{"deepseek-chat", "gpt-4o-mini"}, chain)
}

func TestCostCascadeEmptyChains(t *testing.T) {
	cr := NewCostRouter(map[string][]string{})
	chain := cr.CascadeFor("any-model")
	require.Nil(t, chain)
}

func TestCostCascadeNoDefaultChain(t *testing.T) {
	cr := NewCostRouter(map[string][]string{
		"gpt-4o": {"gpt-4o-mini"},
	})
	chain := cr.CascadeFor("unknown-model")
	require.Nil(t, chain)
}
