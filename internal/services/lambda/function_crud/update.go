package function_crud

import (
	"fmt"
	"net/http"
	"time"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

type updateFunctionCodeRequest struct {
	Architectures   []string `json:"Architectures,omitempty" validate:"omitempty,dive,oneof=x86_64 arm64"`
	DryRun          bool     `json:"DryRun,omitempty"`
	ImageUri        string   `json:"ImageUri,omitempty"`
	Publish         bool     `json:"Publish,omitempty"`
	RevisionId      string   `json:"RevisionId,omitempty"`
	S3Bucket        string   `json:"S3Bucket,omitempty"`
	S3Key           string   `json:"S3Key,omitempty"`
	S3ObjectVersion string   `json:"S3ObjectVersion,omitempty"`
	ZipFile         []byte   `json:"ZipFile,omitempty"`
}

// PUT /2015-03-31/functions/{FunctionName}/code
func (s *Service) UpdateCode(w http.ResponseWriter, r *http.Request, name string) {
	req, ok := jsonhttp.DecodeAndValidate[updateFunctionCodeRequest](w, r)
	if !ok {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	fn, ok := s.functions[name]
	if !ok {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", name))
		return
	}

	if req.RevisionId != "" && req.RevisionId != fn.RevisionId {
		jsonhttp.Error(w, http.StatusPreconditionFailed, "PreconditionFailedException",
			"The RevisionId provided does not match the latest RevisionId for the Lambda function.")
		return
	}

	if req.DryRun {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if len(req.Architectures) > 0 {
		fn.Architectures = req.Architectures
	}
	fn.CodeSize = int64(len(req.ZipFile))
	fn.LastModified = time.Now().UTC().Format(time.RFC3339Nano)
	fn.RevisionId = newRevisionID()

	jsonhttp.Write(w, http.StatusOK, fn)
}

type updateFunctionConfigurationRequest struct {
	DeadLetterConfig  *DeadLetterConfig  `json:"DeadLetterConfig,omitempty"`
	Description       string             `json:"Description,omitempty"`
	Environment       *Environment       `json:"Environment,omitempty"`
	EphemeralStorage  *EphemeralStorage  `json:"EphemeralStorage,omitempty"  validate:"omitempty"`
	FileSystemConfigs []FileSystemConfig `json:"FileSystemConfigs,omitempty" validate:"omitempty,dive"`
	Handler           string             `json:"Handler,omitempty"`
	ImageConfig       *ImageConfig       `json:"ImageConfig,omitempty"`
	KMSKeyArn         string             `json:"KMSKeyArn,omitempty"`
	Layers            []string           `json:"Layers,omitempty"`
	LoggingConfig     *LoggingConfig     `json:"LoggingConfig,omitempty"`
	MemorySize        int                `json:"MemorySize,omitempty"        validate:"omitempty,min=128,max=10240"`
	RevisionId        string             `json:"RevisionId,omitempty"`
	Role              string             `json:"Role,omitempty"`
	Runtime           string             `json:"Runtime,omitempty"`
	SnapStart         *SnapStart         `json:"SnapStart,omitempty"`
	Timeout           int                `json:"Timeout,omitempty"           validate:"omitempty,min=1,max=900"`
	TracingConfig     *TracingConfig     `json:"TracingConfig,omitempty"     validate:"omitempty"`
	VpcConfig         *VpcConfig         `json:"VpcConfig,omitempty"`
}

// PUT /2015-03-31/functions/{FunctionName}/configuration
func (s *Service) UpdateConfiguration(w http.ResponseWriter, r *http.Request, name string) {
	req, ok := jsonhttp.DecodeAndValidate[updateFunctionConfigurationRequest](w, r)
	if !ok {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	fn, ok := s.functions[name]
	if !ok {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", name))
		return
	}

	if req.RevisionId != "" && req.RevisionId != fn.RevisionId {
		jsonhttp.Error(w, http.StatusPreconditionFailed, "PreconditionFailedException",
			"The RevisionId provided does not match the latest RevisionId for the Lambda function.")
		return
	}

	if req.Description != "" {
		fn.Description = req.Description
	}
	if req.Handler != "" {
		fn.Handler = req.Handler
	}
	if req.Role != "" {
		fn.Role = req.Role
	}
	if req.Runtime != "" {
		fn.Runtime = req.Runtime
	}
	if req.MemorySize != 0 {
		fn.MemorySize = req.MemorySize
	}
	if req.Timeout != 0 {
		fn.Timeout = req.Timeout
	}
	if req.KMSKeyArn != "" {
		fn.KMSKeyArn = req.KMSKeyArn
	}
	if req.DeadLetterConfig != nil {
		fn.DeadLetterConfig = req.DeadLetterConfig
	}
	if req.Environment != nil {
		fn.Environment = req.Environment
	}
	if req.EphemeralStorage != nil {
		fn.EphemeralStorage = req.EphemeralStorage
	}
	if req.FileSystemConfigs != nil {
		fn.FileSystemConfigs = req.FileSystemConfigs
	}
	if req.Layers != nil {
		fn.Layers = req.Layers
	}
	if req.LoggingConfig != nil {
		fn.LoggingConfig = req.LoggingConfig
	}
	if req.SnapStart != nil {
		fn.SnapStart = req.SnapStart
	}
	if req.TracingConfig != nil {
		fn.TracingConfig = req.TracingConfig
	}
	if req.VpcConfig != nil {
		fn.VpcConfig = req.VpcConfig
	}

	fn.LastModified = time.Now().UTC().Format(time.RFC3339Nano)
	fn.RevisionId = newRevisionID()

	jsonhttp.Write(w, http.StatusOK, fn)
}
