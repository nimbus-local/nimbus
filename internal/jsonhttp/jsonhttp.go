// Package jsonhttp provides shared HTTP helpers for AWS JSON-protocol services.
// It handles the application/x-amz-json-1.1 content type and error envelope
// that Lambda, SSM, Secrets Manager, and similar services use.
package jsonhttp

import (
	"encoding/json"
	"net/http"
)

// Contract is implemented by request structs that know how to validate themselves.
// Decode automatically calls Validate() if the decoded type satisfies this interface.
type Contract interface {
	Validate() error
}

// Write encodes v as JSON with the AWS JSON content type.
func Write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// Error writes an AWS-shaped JSON error response.
func Error(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.Header().Set("x-amzn-ErrorType", code)
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{
		"__type":  code,
		"message": message,
	})
}

// Decode decodes the JSON request body into T.
// If T implements Contract, Validate() is called automatically.
// Returns (value, true) on success, writes an error response and returns (zero, false) on failure.
func Decode[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var v T
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		Error(w, http.StatusBadRequest, "InvalidParameterValueException",
			"invalid request body: "+err.Error())
		return v, false
	}
	if c, ok := any(&v).(Contract); ok {
		if err := c.Validate(); err != nil {
			Error(w, http.StatusBadRequest, "InvalidParameterValueException", err.Error())
			return v, false
		}
	}
	return v, true
}
