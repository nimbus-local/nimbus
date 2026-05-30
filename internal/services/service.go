package services

import "net/http"

// Service is the interface every Nimbus service must implement.
// Reset() is required so /_nimbus/reset can clear all in-memory state across
// every service in a single call — no orphaned resources between test runs.
type Service interface {
	Name() string
	Detect(r *http.Request) bool
	ServeHTTP(w http.ResponseWriter, r *http.Request)
	// Reset clears all in-memory state. Called by /_nimbus/reset.
	Reset()
}
