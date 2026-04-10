package url_config

type Cors struct {
	AllowCredentials bool     `json:"AllowCredentials,omitempty"`
	AllowHeaders     []string `json:"AllowHeaders,omitempty"`
	AllowMethods     []string `json:"AllowMethods,omitempty"`
	AllowOrigins     []string `json:"AllowOrigins,omitempty"`
	ExposeHeaders    []string `json:"ExposeHeaders,omitempty"`
	MaxAge           int      `json:"MaxAge,omitempty"`
}

type FunctionUrlConfig struct {
	AuthType         string `json:"AuthType"`
	Cors             *Cors  `json:"Cors,omitempty"`
	CreationTime     string `json:"CreationTime"`
	FunctionArn      string `json:"FunctionArn"`
	FunctionUrl      string `json:"FunctionUrl"`
	LastModifiedTime string `json:"LastModifiedTime"`
	// internal
	FunctionName string `json:"-"`
	Qualifier    string `json:"-"`
}

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

type EventInvokeConfig struct {
	DestinationConfig        *DestinationConfig `json:"DestinationConfig,omitempty"`
	FunctionArn              string             `json:"FunctionArn"`
	LastModified             string             `json:"LastModified"`
	MaximumEventAgeInSeconds int                `json:"MaximumEventAgeInSeconds,omitempty"`
	MaximumRetryAttempts     int                `json:"MaximumRetryAttempts,omitempty"`
	// internal
	FunctionName string `json:"-"`
	Qualifier    string `json:"-"`
}
