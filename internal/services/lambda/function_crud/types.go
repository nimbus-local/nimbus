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

// ImageConfigResponse wraps ImageConfig in the FunctionConfiguration response.
// AWS nests the overrides one level deeper on the way out than on the way in,
// and SDK clients read the nested shape — returning a bare ImageConfig leaves
// the field empty on read-back.
type ImageConfigResponse struct {
	Error       *ImageConfigError `json:"Error,omitempty"`
	ImageConfig *ImageConfig      `json:"ImageConfig,omitempty"`
}

// ImageConfigError reports why an image's configuration could not be read.
// Nimbus never populates it — no image is inspected — but it is part of the
// response shape clients deserialize.
type ImageConfigError struct {
	ErrorCode string `json:"ErrorCode,omitempty"`
	Message   string `json:"Message,omitempty"`
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
	Architectures        []string             `json:"Architectures"`
	CodeSha256           string               `json:"CodeSha256"`
	CodeSize             int64                `json:"CodeSize"`
	DeadLetterConfig     *DeadLetterConfig    `json:"DeadLetterConfig,omitempty"`
	Description          string               `json:"Description"`
	Environment          *Environment         `json:"Environment,omitempty"`
	EphemeralStorage     *EphemeralStorage    `json:"EphemeralStorage"`
	FileSystemConfigs    []FileSystemConfig   `json:"FileSystemConfigs,omitempty"`
	FunctionArn          string               `json:"FunctionArn"`
	FunctionName         string               `json:"FunctionName"`
	Handler              string               `json:"Handler"`
	ImageConfigResponse  *ImageConfigResponse `json:"ImageConfigResponse,omitempty"`
	KMSKeyArn            string               `json:"KMSKeyArn,omitempty"`
	LastModified         string               `json:"LastModified"`
	Layers               []string             `json:"Layers,omitempty"`
	LoggingConfig        *LoggingConfig       `json:"LoggingConfig,omitempty"`
	MemorySize           int                  `json:"MemorySize"`
	PackageType          string               `json:"PackageType"`
	RevisionId           string               `json:"RevisionId"`
	Role                 string               `json:"Role"`
	Runtime              string               `json:"Runtime"`
	SnapStart            *SnapStart           `json:"SnapStart,omitempty"`
	State                string               `json:"State"`
	LastUpdateStatus     string               `json:"LastUpdateStatus"`
	LastUpdateStatusCode string               `json:"LastUpdateStatusCode,omitempty"`
	Timeout              int                  `json:"Timeout"`
	TracingConfig        *TracingConfig       `json:"TracingConfig,omitempty"`
	Version              string               `json:"Version"`
	VpcConfig            *VpcConfig           `json:"VpcConfig,omitempty"`

	Tags map[string]string `json:"-"` // stored internally, exposed via ListTags

	// ImageUri is the container image reference for Image package types. AWS
	// reports it in the GetFunction Code block rather than in
	// FunctionConfiguration, so it is stored here but never serialized inline.
	ImageUri string `json:"-"`
}

// Package types accepted by CreateFunction.
const (
	PackageTypeZip   = "Zip"
	PackageTypeImage = "Image"
)

// FunctionCodeLocation is the "Code" block of the GetFunction envelope. Zip
// functions report an S3 repository; container-image functions report the image
// reference, which is where clients read the configured image back from.
type FunctionCodeLocation struct {
	ImageUri         string `json:"ImageUri,omitempty"`
	Location         string `json:"Location,omitempty"`
	RepositoryType   string `json:"RepositoryType"`
	ResolvedImageUri string `json:"ResolvedImageUri,omitempty"`
}

// CodeLocation builds the Code block describing where this function's
// deployment artifact lives.
func (fn *FunctionConfig) CodeLocation() FunctionCodeLocation {
	if fn.PackageType == PackageTypeImage {
		return FunctionCodeLocation{
			ImageUri:         fn.ImageUri,
			RepositoryType:   "ECR",
			ResolvedImageUri: resolveImageURI(fn.ImageUri),
		}
	}
	// Nimbus stores no artifact for Zip functions, so there is no download URL
	// to presign and Location is omitted.
	return FunctionCodeLocation{RepositoryType: "S3"}
}
