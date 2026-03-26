package router

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/nimbus-local/nimbus/internal/services"
)

// Router is the edge router. All traffic enters on :4566 (matching LocalStack's
// default port) and is dispatched to the appropriate service based on request
// characteristics. Services register themselves; the router has no hardcoded
// knowledge of any specific service.
type Router struct {
	services []services.Service
	logger   *slog.Logger
}

func New(logger *slog.Logger) *Router {
	return &Router{logger: logger}
}

// Register adds a service to the router. Services are checked in registration
// order, so more specific detectors should be registered first.
func (r *Router) Register(svc services.Service) {
	r.services = append(r.services, svc)
	r.logger.Info("registered service", "service", svc.Name())
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// Inject standard AWS-compatible response headers
	w.Header().Set("x-amz-request-id", newRequestID())
	w.Header().Set("x-amz-id-2", newRequestID())

	for _, svc := range r.services {
		if svc.Detect(req) {
			r.logger.Debug("routing request",
				"service", svc.Name(),
				"method", req.Method,
				"path", req.URL.Path,
			)
			svc.ServeHTTP(w, req)
			return
		}
	}

	r.logger.Warn("no service matched request",
		"method", req.Method,
		"path", req.URL.Path,
		"host", req.Host,
	)
	http.Error(w, "no service matched this request", http.StatusBadRequest)
}

// Health endpoint — used by Docker healthcheck
func (r *Router) HealthHandler(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status":"running","services":%s}`, r.serviceList())
}

func (r *Router) serviceList() string {
	out := "["
	for i, svc := range r.services {
		if i > 0 {
			out += ","
		}
		out += `"` + svc.Name() + `"`
	}
	return out + "]"
}

func newRequestID() string {
	// Simple incrementing request ID — good enough for local dev
	return fmt.Sprintf("%016x", requestCounter.Add(1))
}
