package lambda

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/nimbus-local/nimbus/internal/uid"
)

var validate = validator.New()

// --- Request / response types ---

type FunctionCode struct {
	ImageUri        string `json:"ImageUri,omitempty"`
	S3Bucket        string `json:"S3Bucket,omitempty"`
	S3Key           string `json:"S3Key,omitempty"`
	S3ObjectVersion string `json:"S3ObjectVersion,omitempty"`
	ZipFile         []byte `json:"ZipFile,omitempty"` // base64-encoded by the SDK
}

type DeadLetterConfig struct {
	TargetArn string `json:"TargetArn,omitempty"`
}

type Environment struct {
	Variables map[string]string `json:"Variables,omitempty"`
}

type EphemeralStorage struct {
	Size int `json:"Size,omitempty" validate:"omitempty,min=512,max=10240"`
}

type FileSystemConfig struct {
	Arn            string `json:"Arn"            validate:"required"`
	LocalMountPath string `json:"LocalMountPath" validate:"required"`
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
	Mode string `json:"Mode,omitempty" validate:"omitempty,oneof=Active PassThrough"`
}

type VpcConfig struct {
	Ipv6AllowedForDualStack bool     `json:"Ipv6AllowedForDualStack,omitempty"`
	SecurityGroupIds        []string `json:"SecurityGroupIds,omitempty"`
	SubnetIds               []string `json:"SubnetIds,omitempty"`
}

type CreateFunctionRequest struct {
	// Required
	Code         FunctionCode `json:"Code"`
	FunctionName string       `json:"FunctionName" validate:"required"`
	Role         string       `json:"Role"         validate:"required"`

	// Required unless PackageType=Image
	Handler string `json:"Handler,omitempty" validate:"required_unless=PackageType Image"`
	Runtime string `json:"Runtime,omitempty" validate:"required_unless=PackageType Image"`

	// Optional
	Architectures        []string           `json:"Architectures,omitempty"        validate:"omitempty,dive,oneof=x86_64 arm64"`
	CodeSigningConfigArn string             `json:"CodeSigningConfigArn,omitempty"`
	DeadLetterConfig     *DeadLetterConfig  `json:"DeadLetterConfig,omitempty"`
	Description          string             `json:"Description,omitempty"`
	Environment          *Environment       `json:"Environment,omitempty"`
	EphemeralStorage     *EphemeralStorage  `json:"EphemeralStorage,omitempty"     validate:"omitempty"`
	FileSystemConfigs    []FileSystemConfig `json:"FileSystemConfigs,omitempty"    validate:"omitempty,dive"`
	ImageConfig          *ImageConfig       `json:"ImageConfig,omitempty"`
	KMSKeyArn            string             `json:"KMSKeyArn,omitempty"`
	Layers               []string           `json:"Layers,omitempty"`
	LoggingConfig        *LoggingConfig     `json:"LoggingConfig,omitempty"`
	MemorySize           int                `json:"MemorySize,omitempty"           validate:"omitempty,min=128,max=10240"`
	PackageType          string             `json:"PackageType,omitempty"          validate:"omitempty,oneof=Zip Image"`
	Publish              bool               `json:"Publish,omitempty"`
	SnapStart            *SnapStart         `json:"SnapStart,omitempty"`
	Tags                 map[string]string  `json:"Tags,omitempty"`
	Timeout              int                `json:"Timeout,omitempty"             validate:"omitempty,min=1,max=900"`
	TracingConfig        *TracingConfig     `json:"TracingConfig,omitempty"        validate:"omitempty"`
	VpcConfig            *VpcConfig         `json:"VpcConfig,omitempty"`
}

// functionConfig is the in-memory representation of a Lambda function.
type functionConfig struct {
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

	tags map[string]string
}

// POST /2015-03-31/functions
func (s *Service) createFunction(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeAndValidate[CreateFunctionRequest](w, r)
	if !ok {
		return
	}

	req.applyDefaults()

	arn := s.functionARN(req.FunctionName)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.functions[req.FunctionName]; exists {
		jsonError(w, http.StatusConflict, "ResourceConflictException",
			fmt.Sprintf("Function already exist: %s", arn))
		return
	}

	fn := newFunctionConfig(req, arn)
	s.functions[req.FunctionName] = fn

	jsonWrite(w, http.StatusCreated, fn)
}

// decodeAndValidate decodes the JSON request body into T and runs validation.
// Returns (value, true) on success, writes an error response and returns (zero, false) on failure.
func decodeAndValidate[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var v T
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		jsonError(w, http.StatusBadRequest, "InvalidParameterValueException",
			"invalid request body: "+err.Error())
		return v, false
	}
	if err := validate.Struct(v); err != nil {
		var ve validator.ValidationErrors
		if errors.As(err, &ve) {
			jsonError(w, http.StatusBadRequest, "InvalidParameterValueException",
				validationMessage(ve))
			return v, false
		}
		jsonError(w, http.StatusBadRequest, "InvalidParameterValueException", err.Error())
		return v, false
	}
	return v, true
}

// applyDefaults fills in AWS defaults for omitted fields.
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

func (s *Service) functionARN(name string) string {
	return fmt.Sprintf("arn:aws:lambda:%s:%s:function:%s", s.region, s.account, name)
}

func newFunctionConfig(req CreateFunctionRequest, arn string) *functionConfig {
	return &functionConfig{
		Architectures:     req.Architectures,
		CodeSha256:        "", // would be computed from ZipFile / S3 object in a real impl
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
		tags:              req.Tags,
	}
}

// validationMessage turns ValidationErrors into a single readable string,
// using the JSON field name where possible.
func validationMessage(ve validator.ValidationErrors) string {
	msgs := make([]string, 0, len(ve))
	for _, fe := range ve {
		msgs = append(msgs, fmt.Sprintf("%s: failed '%s' validation", fe.Field(), fe.Tag()))
	}
	return strings.Join(msgs, "; ")
}

// --- JSON helpers ---

func jsonWrite(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, status int, code, message string) {
	type errResp struct {
		Code    string `json:"__type"`
		Message string `json:"message"`
	}
	jsonWrite(w, status, errResp{Code: code, Message: message})
}

func newRevisionID() string {
	return uid.New()
}
