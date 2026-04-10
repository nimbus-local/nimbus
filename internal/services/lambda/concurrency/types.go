package concurrency

// ProvisionedConcurrencyConfig holds the state for a provisioned concurrency configuration.
type ProvisionedConcurrencyConfig struct {
	AllocatedProvisionedConcurrentExecutions int    `json:"AllocatedProvisionedConcurrentExecutions"`
	AvailableProvisionedConcurrentExecutions int    `json:"AvailableProvisionedConcurrentExecutions"`
	FunctionArn                              string `json:"FunctionArn"`
	LastModified                             string `json:"LastModified"`
	RequestedProvisionedConcurrentExecutions int    `json:"RequestedProvisionedConcurrentExecutions"`
	StatusReason                             string `json:"StatusReason,omitempty"`
	Status                                   string `json:"Status"` // "IN_PROGRESS" | "READY" | "FAILED"
}
