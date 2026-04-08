package lambda

import (
	"net/http"
	"strings"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
	"github.com/nimbus-local/nimbus/internal/services/lambda/function_crud"
	"github.com/nimbus-local/nimbus/internal/services/lambda/invocation"
)

const defaultAccount = "000000000000"

// Service is the top-level Lambda emulator. It owns routing and composes
// the sub-package services that implement each group of API operations.
type Service struct {
	region     string
	account    string
	CRUD       *function_crud.Service
	Invocation *invocation.Service
}

func New(region string) *Service {
	if region == "" {
		region = "us-east-1"
	}
	crud := function_crud.New(region, defaultAccount)
	return &Service{
		region:     region,
		account:    defaultAccount,
		CRUD:       crud,
		Invocation: invocation.New(crud),
	}
}

func (s *Service) Name() string { return "lambda" }

// Detect identifies Lambda requests by the /2015-03-31/ path prefix used by
// the Lambda REST API. This is distinct from services that use X-Amz-Target.
func (s *Service) Detect(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/2015-03-31/")
}

// ServeHTTP routes to the appropriate sub-package handler.
// Lambda uses a REST API: method + path determine the operation.
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/2015-03-31")

	// Collection-level: /functions
	switch {
	case r.Method == http.MethodPost && path == "/functions":
		s.CRUD.Create(w, r)
		return
	case r.Method == http.MethodGet && path == "/functions":
		s.CRUD.List(w, r)
		return
	}

	// Resource-level: /functions/{name}[/suffix]
	rest := strings.TrimPrefix(path, "/functions/")
	if rest == path {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			"Unknown operation for path: "+r.URL.Path)
		return
	}

	name, suffix, _ := strings.Cut(rest, "/")
	if name == "" {
		jsonhttp.Error(w, http.StatusBadRequest, "ValidationException", "FunctionName is required")
		return
	}
	suffix = strings.TrimSuffix(suffix, "/") // normalize invoke-async trailing slash

	switch {
	case r.Method == http.MethodGet && suffix == "":
		s.CRUD.Get(w, r, name)
	case r.Method == http.MethodGet && suffix == "configuration":
		s.CRUD.GetConfiguration(w, r, name)
	case r.Method == http.MethodPut && suffix == "code":
		s.CRUD.UpdateCode(w, r, name)
	case r.Method == http.MethodPut && suffix == "configuration":
		s.CRUD.UpdateConfiguration(w, r, name)
	case r.Method == http.MethodDelete && suffix == "":
		s.CRUD.Delete(w, r, name)
	case r.Method == http.MethodGet && suffix == "versions":
		s.CRUD.ListVersions(w, r, name)
	case r.Method == http.MethodPost && suffix == "versions":
		s.CRUD.PublishVersion(w, r, name)
	case r.Method == http.MethodPost && suffix == "invocations":
		s.Invocation.Invoke(w, r, name)
	case r.Method == http.MethodPost && suffix == "invoke-async":
		s.Invocation.InvokeAsync(w, r, name)
	case r.Method == http.MethodPost && suffix == "response-streaming-invocations":
		s.Invocation.InvokeWithResponseStream(w, r, name)
	default:
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			"Unknown operation for path: "+r.URL.Path)
	}
}
