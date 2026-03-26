package dynamodb

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// Service proxies DynamoDB requests to the official DynamoDB Local JAR,
// which runs as a sidecar container. This gives us perfect AWS parity
// for DynamoDB without reimplementing it — AWS maintains it themselves.
//
// DynamoDB Local image: amazon/dynamodb-local
// Default endpoint:     http://dynamodb-local:8000
type Service struct {
	proxy    *httputil.ReverseProxy
	endpoint string
	logger   *slog.Logger
}

func New(endpoint string, logger *slog.Logger) *Service {
	if endpoint == "" {
		endpoint = "http://dynamodb-local:8000"
	}

	target, err := url.Parse(endpoint)
	if err != nil {
		panic(fmt.Sprintf("invalid DynamoDB endpoint: %s", err))
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		logger.Error("dynamodb proxy error", "err", err)
		http.Error(w, `{"__type":"InternalFailure","message":"DynamoDB Local unavailable"}`,
			http.StatusServiceUnavailable)
	}

	return &Service{
		proxy:    proxy,
		endpoint: endpoint,
		logger:   logger,
	}
}

func (s *Service) Name() string { return "dynamodb" }

// Detect identifies DynamoDB requests by the X-Amz-Target header prefix.
// All DynamoDB operations use DynamoDB_20120810.<OperationName>
func (s *Service) Detect(r *http.Request) bool {
	target := r.Header.Get("X-Amz-Target")
	return strings.HasPrefix(target, "DynamoDB_")
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.logger.Debug("proxying to DynamoDB Local",
		"method", r.Method,
		"target", r.Header.Get("X-Amz-Target"),
	)

	// Read and re-set the body so the proxy can forward it
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "could not read request body", http.StatusInternalServerError)
		return
	}
	r.Body = io.NopCloser(strings.NewReader(string(body)))
	r.ContentLength = int64(len(body))

	s.proxy.ServeHTTP(w, r)
}

// HealthCheck pings DynamoDB Local to see if it's available
func (s *Service) HealthCheck() bool {
	resp, err := http.Get(s.endpoint)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return true
}
