package code_signing

type AllowedPublishers struct {
	SigningProfileVersionArns []string `json:"SigningProfileVersionArns"`
}

type CodeSigningPolicies struct {
	UntrustedArtifactOnDeployment string `json:"UntrustedArtifactOnDeployment,omitempty"` // "Warn" | "Enforce"
}

type CodeSigningConfig struct {
	AllowedPublishers    AllowedPublishers   `json:"AllowedPublishers"`
	CodeSigningConfigArn string              `json:"CodeSigningConfigArn"`
	CodeSigningConfigId  string              `json:"CodeSigningConfigId"`
	CodeSigningPolicies  CodeSigningPolicies `json:"CodeSigningPolicies"`
	Description          string              `json:"Description,omitempty"`
	LastModified         string              `json:"LastModified"`
}
