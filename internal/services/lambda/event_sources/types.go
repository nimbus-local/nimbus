package event_sources

type DestinationConfig struct {
	OnFailure *OnFailureDestination `json:"OnFailure,omitempty"`
	OnSuccess *OnSuccessDestination `json:"OnSuccess,omitempty"`
}

type OnFailureDestination struct {
	Destination string `json:"Destination,omitempty"`
}

type OnSuccessDestination struct {
	Destination string `json:"Destination,omitempty"`
}

type FilterCriteria struct {
	Filters []Filter `json:"Filters,omitempty"`
}

type Filter struct {
	Pattern string `json:"Pattern,omitempty"`
}

type EventSourceMapping struct {
	BatchSize                      int                `json:"BatchSize,omitempty"`
	BisectBatchOnFunctionError     bool               `json:"BisectBatchOnFunctionError,omitempty"`
	DestinationConfig              *DestinationConfig `json:"DestinationConfig,omitempty"`
	EventSourceArn                 string             `json:"EventSourceArn,omitempty"`
	FilterCriteria                 *FilterCriteria    `json:"FilterCriteria,omitempty"`
	FunctionArn                    string             `json:"FunctionArn"`
	FunctionName                   string             `json:"-"` // internal only
	LastModified                   float64            `json:"LastModified"`
	MaximumBatchingWindowInSeconds int                `json:"MaximumBatchingWindowInSeconds,omitempty"`
	MaximumRecordAgeInSeconds      int                `json:"MaximumRecordAgeInSeconds,omitempty"`
	MaximumRetryAttempts           int                `json:"MaximumRetryAttempts,omitempty"`
	ParallelizationFactor          int                `json:"ParallelizationFactor,omitempty"`
	StartingPosition               string             `json:"StartingPosition,omitempty"`
	StartingPositionTimestamp      float64            `json:"StartingPositionTimestamp,omitempty"`
	State                          string             `json:"State"`
	StateTransitionReason          string             `json:"StateTransitionReason"`
	TumblingWindowInSeconds        int                `json:"TumblingWindowInSeconds,omitempty"`
	UUID                           string             `json:"UUID"`
}
