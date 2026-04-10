package function_crud

// Shared types used across multiple function CRUD operations.
// Types exclusive to a single operation live in that operation's file.

type DeadLetterConfig struct {
	TargetArn string `json:"TargetArn,omitempty"`
}

type Environment struct {
	Variables map[string]string `json:"Variables,omitempty"`
}

type EphemeralStorage struct {
	Size int `json:"Size,omitempty"`
}

type FileSystemConfig struct {
	Arn            string `json:"Arn"`
	LocalMountPath string `json:"LocalMountPath"`
}

type ImageConfig struct {
	Command          []string `json:"Command,omitempty"`
	EntryPoint       []string `json:"EntryPoint,omitempty"`
	WorkingDirectory string   `json:"WorkingDirectory,omitempty"`
}

type LoggingConfig struct {
	ApplicationLogLevel string `json:"ApplicationLogLevel,omitempty"`
	LogFormat           string `json:"LogFormat,omitempty"`
	LogGroup            string `json:"LogGroup,omitempty"`
	SystemLogLevel      string `json:"SystemLogLevel,omitempty"`
}

type SnapStart struct {
	ApplyOn string `json:"ApplyOn,omitempty"` // "PublishedVersions" | "None"
}

type TracingConfig struct {
	Mode string `json:"Mode,omitempty"` // "Active" | "PassThrough"
}

type VpcConfig struct {
	Ipv6AllowedForDualStack bool     `json:"Ipv6AllowedForDualStack,omitempty"`
	SecurityGroupIds        []string `json:"SecurityGroupIds,omitempty"`
	SubnetIds               []string `json:"SubnetIds,omitempty"`
}

// FunctionConfig is the in-memory representation of a Lambda function.
// It is also the response shape for CreateFunction, GetFunction, and ListFunctions.
type FunctionConfig struct {
	Architectures     []string           `json:"Architectures"`
	CodeSha256        string             `json:"CodeSha256"`
	CodeSize          int64              `json:"CodeSize"`
	DeadLetterConfig  *DeadLetterConfig  `json:"DeadLetterConfig,omitempty"`
	Description       string             `json:"Description"`
	Environment       *Environment       `json:"Environment,omitempty"`
	EphemeralStorage  *EphemeralStorage  `json:"EphemeralStorage"`
	FileSystemConfigs []FileSystemConfig `json:"FileSystemConfigs,omitempty"`
	FunctionArn       string             `json:"FunctionArn"`
	FunctionName      string             `json:"FunctionName"`
	Handler           string             `json:"Handler"`
	KMSKeyArn         string             `json:"KMSKeyArn,omitempty"`
	LastModified      string             `json:"LastModified"`
	Layers            []string           `json:"Layers,omitempty"`
	LoggingConfig     *LoggingConfig     `json:"LoggingConfig,omitempty"`
	MemorySize        int                `json:"MemorySize"`
	PackageType       string             `json:"PackageType"`
	RevisionId        string             `json:"RevisionId"`
	Role              string             `json:"Role"`
	Runtime           string             `json:"Runtime"`
	SnapStart         *SnapStart         `json:"SnapStart,omitempty"`
	State             string             `json:"State"`
	Timeout           int                `json:"Timeout"`
	TracingConfig     *TracingConfig     `json:"TracingConfig,omitempty"`
	Version           string             `json:"Version"`
	VpcConfig         *VpcConfig         `json:"VpcConfig,omitempty"`

	Tags map[string]string `json:"-"` // stored internally, exposed via ListTags
}
