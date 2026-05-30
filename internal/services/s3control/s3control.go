// Package s3control emulates the AWS S3 Control management plane.
// S3 Control handles account-level S3 operations that use /v20180820/ paths
// and the x-amz-account-id request header, distinct from the regular S3 API.
//
// Pulumi AWS provider v7 calls s3control:ListTagsForResource / PutTags /
// DeleteTags after every s3:Bucket create/update to reconcile resource tags.
// This stub returns empty tags on GET and accepts PUT/DELETE as no-ops so the
// provider does not error on those calls.
package s3control

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Service implements the S3 Control emulator.
type Service struct{}

func New() *Service { return &Service{} }

func (s *Service) Name() string { return "s3control" }

// Reset is a no-op: s3control has no in-memory state.
func (s *Service) Reset() {}

// Detect identifies S3 Control requests by the x-amz-account-id header or
// the /v20180820/ path prefix used by the S3 Control API.
func (s *Service) Detect(r *http.Request) bool {
	return r.Header.Get("x-amz-account-id") != "" ||
		strings.HasPrefix(r.URL.Path, "/v20180820/")
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Tags: /v20180820/tags/{arn}
	if strings.HasPrefix(r.URL.Path, "/v20180820/tags") {
		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode(map[string]interface{}{"Tags": []interface{}{}})
		case http.MethodPut:
			w.WriteHeader(http.StatusOK)
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}

	// Public access block: /v20180820/configuration/publicAccessBlock
	if strings.HasPrefix(r.URL.Path, "/v20180820/configuration/publicAccessBlock") {
		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode(map[string]interface{}{
				"PublicAccessBlockConfiguration": map[string]bool{
					"BlockPublicAcls":       true,
					"BlockPublicPolicy":     true,
					"IgnorePublicAcls":      true,
					"RestrictPublicBuckets": true,
				},
			})
		case http.MethodPut, http.MethodDelete:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}

	// All other S3 Control paths — accept and no-op
	w.WriteHeader(http.StatusOK)
}
