package aliases

// AliasRoutingConfiguration holds weighted routing between two function versions.
type AliasRoutingConfiguration struct {
	AdditionalVersionWeights map[string]float64 `json:"AdditionalVersionWeights,omitempty"`
}

// AliasConfig is the canonical representation of a Lambda alias.
type AliasConfig struct {
	AliasArn        string                     `json:"AliasArn"`
	Description     string                     `json:"Description"`
	FunctionVersion string                     `json:"FunctionVersion"`
	Name            string                     `json:"Name"`
	RevisionId      string                     `json:"RevisionId"`
	RoutingConfig   *AliasRoutingConfiguration `json:"RoutingConfig,omitempty"`
}
