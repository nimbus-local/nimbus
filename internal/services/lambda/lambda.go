package lambda

import (
	"net/http"
	"strings"
	"sync"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

const (
	defaultAccount = "000000000000"
)

// Service implements the AWS Lambda service.
type Service struct {
	mu        sync.RWMutex
	region    string
	account   string
	functions map[string]*functionConfig // keyed by function name
}

func (s *Service) Name() string { return "lambda" }

func New(region string) *Service {
	if region == "" {
		region = "us-east-1"
	}
	return &Service{
		region:    region,
		account:   defaultAccount,
		functions: map[string]*functionConfig{},
	}
}

// Lambda uses a REST API — route by method + path, not X-Amz-Target.
// Base path: /2015-03-31/functions[/{FunctionName}[/...]]
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/2015-03-31")

	switch {
	case r.Method == http.MethodPost && path == "/functions":
		s.createFunction(w, r)
	case r.Method == http.MethodGet && path == "/functions":
		s.listFunctions(w, r)
	default:
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			"Unknown operation for path: "+r.URL.Path)
	}
}
