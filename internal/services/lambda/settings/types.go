package settings

// AccountLimit holds the fixed mock limits for this account.
type AccountLimit struct {
	CodeSizeUnzipped               int64 `json:"CodeSizeUnzipped"`
	CodeSizeZipped                 int64 `json:"CodeSizeZipped"`
	ConcurrentExecutions           int   `json:"ConcurrentExecutions"`
	TotalCodeSize                  int64 `json:"TotalCodeSize"`
	UnreservedConcurrentExecutions int   `json:"UnreservedConcurrentExecutions"`
}

// AccountUsage holds mock usage figures for this account.
type AccountUsage struct {
	FunctionCount int64 `json:"FunctionCount"`
	TotalCodeSize int64 `json:"TotalCodeSize"`
}

// RuntimeManagementConfig holds the runtime update policy for a single function.
type RuntimeManagementConfig struct {
	FunctionArn       string `json:"FunctionArn"`
	RuntimeVersionArn string `json:"RuntimeVersionArn,omitempty"`
	UpdateRuntimeOn   string `json:"UpdateRuntimeOn"` // "Auto" | "FunctionUpdate" | "Manual"
}
