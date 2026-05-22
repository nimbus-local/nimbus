package dynamodb

import (
	"bytes"
	"encoding/json"
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
	target := r.Header.Get("X-Amz-Target")
	s.logger.Debug("proxying to DynamoDB Local",
		"method", r.Method,
		"target", target,
	)

	// Read and re-set the body so the proxy can forward it.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "could not read request body", http.StatusInternalServerError)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))

	// For DescribeTable responses, capture the output and inject WarmThroughput.
	// The AWS provider v6 calls output.WarmThroughput.Status in its waiter;
	// DynamoDB Local doesn't return this field, so the waiter never sees ACTIVE.
	if target == "DynamoDB_20120810.DescribeTable" {
		rec := &captureWriter{header: make(http.Header), status: http.StatusOK}
		s.proxy.ServeHTTP(rec, r)
		patched := injectWarmThroughput(rec.body)
		for k, vs := range rec.header {
			for _, v := range vs {
				w.Header().Set(k, v)
			}
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(patched)))
		w.WriteHeader(rec.status)
		w.Write(patched) //nolint:errcheck
		return
	}

	s.proxy.ServeHTTP(w, r)
}

// captureWriter buffers an HTTP response for post-processing.
type captureWriter struct {
	header http.Header
	status int
	body   []byte
}

func (c *captureWriter) Header() http.Header { return c.header }
func (c *captureWriter) WriteHeader(s int)   { c.status = s }
func (c *captureWriter) Write(b []byte) (int, error) {
	c.body = append(c.body, b...)
	return len(b), nil
}

// injectWarmThroughput adds WarmThroughput to the Table or TableDescription
// object if absent. DescribeTable uses "Table"; CreateTable uses "TableDescription".
func injectWarmThroughput(body []byte) []byte {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return body
	}
	// Prefer "Table" (DescribeTable response), fall back to "TableDescription" (CreateTable).
	key := "Table"
	td, ok := obj[key]
	if !ok {
		key = "TableDescription"
		td, ok = obj[key]
	}
	if !ok {
		return body
	}
	var tableDesc map[string]json.RawMessage
	if err := json.Unmarshal(td, &tableDesc); err != nil {
		return body
	}
	if _, exists := tableDesc["WarmThroughput"]; exists {
		return body
	}
	wt, _ := json.Marshal(map[string]any{
		"ReadUnitsPerSecond":  0,
		"WriteUnitsPerSecond": 0,
		"Status":              "ACTIVE",
	})
	tableDesc["WarmThroughput"] = wt
	patched, err := json.Marshal(tableDesc)
	if err != nil {
		return body
	}
	obj[key] = patched
	out, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return out
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
