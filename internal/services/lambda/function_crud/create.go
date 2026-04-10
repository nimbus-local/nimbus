package function_crud

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

// FunctionCode specifies the deployment artifact for a new function.
// Only used on CreateFunction — updates use a separate UpdateFunctionCode API.
type FunctionCode struct {
	ImageUri        string `json:"ImageUri,omitempty"`
	S3Bucket        string `json:"S3Bucket,omitempty"`
	S3Key           string `json:"S3Key,omitempty"`
	S3ObjectVersion string `json:"S3ObjectVersion,omitempty"`
	ZipFile         []byte `json:"ZipFile,omitempty"` // base64-encoded by the SDK
}

type CreateFunctionRequest struct {
	Code                 FunctionCode       `json:"Code"`
	FunctionName         string             `json:"FunctionName"`
	Role                 string             `json:"Role"`
	Handler              string             `json:"Handler,omitempty"`
	Runtime              string             `json:"Runtime,omitempty"`
	Architectures        []string           `json:"Architectures,omitempty"`
	CodeSigningConfigArn string             `json:"CodeSigningConfigArn,omitempty"`
	DeadLetterConfig     *DeadLetterConfig  `json:"DeadLetterConfig,omitempty"`
	Description          string             `json:"Description,omitempty"`
	Environment          *Environment       `json:"Environment,omitempty"`
	EphemeralStorage     *EphemeralStorage  `json:"EphemeralStorage,omitempty"`
	FileSystemConfigs    []FileSystemConfig `json:"FileSystemConfigs,omitempty"`
	ImageConfig          *ImageConfig       `json:"ImageConfig,omitempty"`
	KMSKeyArn            string             `json:"KMSKeyArn,omitempty"`
	Layers               []string           `json:"Layers,omitempty"`
	LoggingConfig        *LoggingConfig     `json:"LoggingConfig,omitempty"`
	MemorySize           int                `json:"MemorySize,omitempty"`
	PackageType          string             `json:"PackageType,omitempty"`
	Publish              bool               `json:"Publish,omitempty"`
	SnapStart            *SnapStart         `json:"SnapStart,omitempty"`
	Tags                 map[string]string  `json:"Tags,omitempty"`
	Timeout              int                `json:"Timeout,omitempty"`
	TracingConfig        *TracingConfig     `json:"TracingConfig,omitempty"`
	VpcConfig            *VpcConfig         `json:"VpcConfig,omitempty"`
}

func (r *CreateFunctionRequest) Validate() error {
	if r.FunctionName == "" {
		return errors.New("FunctionName is required")
	}
	if r.Role == "" {
		return errors.New("Role is required")
	}
	if r.PackageType != "Image" {
		if r.Handler == "" {
			return errors.New("Handler is required for Zip package type")
		}
		if r.Runtime == "" {
			return errors.New("Runtime is required for Zip package type")
		}
	}
	if r.PackageType != "" && r.PackageType != "Zip" && r.PackageType != "Image" {
		return errors.New("PackageType must be Zip or Image")
	}
	for _, arch := range r.Architectures {
		if arch != "x86_64" && arch != "arm64" {
			return errors.New("Architectures must be x86_64 or arm64")
		}
	}
	if r.MemorySize != 0 && (r.MemorySize < 128 || r.MemorySize > 10240) {
		return errors.New("MemorySize must be between 128 and 10240")
	}
	if r.Timeout != 0 && (r.Timeout < 1 || r.Timeout > 900) {
		return errors.New("Timeout must be between 1 and 900")
	}
	return nil
}

// POST /2015-03-31/functions
func (s *Service) Create(w http.ResponseWriter, r *http.Request) {
	req, ok := jsonhttp.Decode[CreateFunctionRequest](w, r)
	if !ok {
		return
	}

	req.applyDefaults()
	arn := s.arn(req.FunctionName)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.functions[req.FunctionName]; exists {
		jsonhttp.Error(w, http.StatusConflict, "ResourceConflictException",
			fmt.Sprintf("Function already exist: %s", arn))
		return
	}

	fn := newFunctionConfig(req, arn)
	s.functions[req.FunctionName] = fn

	jsonhttp.Write(w, http.StatusCreated, fn)
}

func (req *CreateFunctionRequest) applyDefaults() {
	if req.PackageType == "" {
		req.PackageType = "Zip"
	}
	if len(req.Architectures) == 0 {
		req.Architectures = []string{"x86_64"}
	}
	if req.MemorySize == 0 {
		req.MemorySize = 128
	}
	if req.Timeout == 0 {
		req.Timeout = 3
	}
	if req.EphemeralStorage == nil {
		req.EphemeralStorage = &EphemeralStorage{Size: 512}
	}
}

func newFunctionConfig(req CreateFunctionRequest, arn string) *FunctionConfig {
	return &FunctionConfig{
		Architectures:     req.Architectures,
		CodeSha256:        "",
		CodeSize:          int64(len(req.Code.ZipFile)),
		DeadLetterConfig:  req.DeadLetterConfig,
		Description:       req.Description,
		Environment:       req.Environment,
		EphemeralStorage:  req.EphemeralStorage,
		FileSystemConfigs: req.FileSystemConfigs,
		FunctionArn:       arn,
		FunctionName:      req.FunctionName,
		Handler:           req.Handler,
		KMSKeyArn:         req.KMSKeyArn,
		LastModified:      time.Now().UTC().Format(time.RFC3339Nano),
		Layers:            req.Layers,
		LoggingConfig:     req.LoggingConfig,
		MemorySize:        req.MemorySize,
		PackageType:       req.PackageType,
		RevisionId:        newRevisionID(),
		Role:              req.Role,
		Runtime:           req.Runtime,
		SnapStart:         req.SnapStart,
		State:             "Active",
		Timeout:           req.Timeout,
		TracingConfig:     req.TracingConfig,
		Version:           "$LATEST",
		VpcConfig:         req.VpcConfig,
		Tags:              req.Tags,
	}
}
