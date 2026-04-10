package permissions

// Statement represents a single entry in a Lambda resource-based policy.
type Statement struct {
	Sid       string                       `json:"Sid"`
	Effect    string                       `json:"Effect"`
	Principal any                          `json:"Principal"` // string or {"Service": "..."} etc.
	Action    string                       `json:"Action"`
	Condition map[string]map[string]string `json:"Condition,omitempty"`
}

// policyDocument is the full IAM policy document returned by GetPolicy / GetLayerVersionPolicy.
type policyDocument struct {
	Version   string      `json:"Version"`
	Statement []Statement `json:"Statement"`
}
