package decision

type CostRouter struct {
	chains map[string][]string
}

func NewCostRouter(chains map[string][]string) *CostRouter {
	return &CostRouter{chains: chains}
}

func (cr *CostRouter) CascadeFor(model string) []string {
	if chain, ok := cr.chains[model]; ok && len(chain) > 0 {
		return chain
	}
	if chain, ok := cr.chains["default"]; ok && len(chain) > 0 {
		return chain
	}
	return nil
}
