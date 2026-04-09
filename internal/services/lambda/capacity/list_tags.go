package capacity

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

// GET /2015-03-31/tags/{ARN}
func (s *Service) ListTags(w http.ResponseWriter, r *http.Request, resourceArn string) {
	name := functionNameFromARN(resourceArn)
	tags, ok := s.store.GetTags(name)
	if !ok {
		jsonhttp.Error(w, http.StatusNotFound, "ResourceNotFoundException",
			fmt.Sprintf("Function not found: %s", name))
		return
	}
	if tags == nil {
		tags = map[string]string{}
	}
	jsonhttp.Write(w, http.StatusOK, map[string]any{"Tags": tags})
}

// functionNameFromARN extracts the function name from a Lambda ARN like
// arn:aws:lambda:us-east-1:000000000000:function:my-func
func functionNameFromARN(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) == 0 {
		return arn
	}
	return parts[len(parts)-1]
}
