// Package jsonhttp provides shared HTTP helpers for AWS JSON-protocol services.
// It handles the application/x-amz-json-1.1 content type and error envelope
// that Lambda, SSM, Secrets Manager, and similar services use.
package jsonhttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-playground/validator/v10"
)

var validate = validator.New()

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

// DecodeAndValidate decodes the JSON request body into T and runs validation.
// Returns (value, true) on success, writes an error response and returns (zero, false) on failure.
func DecodeAndValidate[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var v T
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		Error(w, http.StatusBadRequest, "InvalidParameterValueException",
			"invalid request body: "+err.Error())
		return v, false
	}
	if err := validate.Struct(v); err != nil {
		var ve validator.ValidationErrors
		if errors.As(err, &ve) {
			Error(w, http.StatusBadRequest, "InvalidParameterValueException",
				ValidationMessage(ve))
			return v, false
		}
		Error(w, http.StatusBadRequest, "InvalidParameterValueException", err.Error())
		return v, false
	}
	return v, true
}

// ValidationMessage turns ValidationErrors into a single readable string.
func ValidationMessage(ve validator.ValidationErrors) string {
	msgs := make([]string, 0, len(ve))
	for _, fe := range ve {
		msgs = append(msgs, fmt.Sprintf("%s: failed '%s' validation", fe.Field(), fe.Tag()))
	}
	return strings.Join(msgs, "; ")
}
