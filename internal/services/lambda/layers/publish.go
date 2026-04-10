package layers

import (
	"errors"
	"net/http"
	"time"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

type publishRequest struct {
	CompatibleArchitectures []string                 `json:"CompatibleArchitectures,omitempty"`
	CompatibleRuntimes      []string                 `json:"CompatibleRuntimes,omitempty"`
	Content                 LayerVersionContentInput `json:"Content"`
	Description             string                   `json:"Description,omitempty"`
	LicenseInfo             string                   `json:"LicenseInfo,omitempty"`
}

func (r *publishRequest) Validate() error {
	for _, arch := range r.CompatibleArchitectures {
		if arch != "x86_64" && arch != "arm64" {
			return errors.New("CompatibleArchitectures must be x86_64 or arm64")
		}
	}
	return nil
}

// POST /2015-03-31/layers/{LayerName}/versions
func (s *Service) Publish(w http.ResponseWriter, r *http.Request, layerName string) {
	req, ok := jsonhttp.Decode[publishRequest](w, r)
	if !ok {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.versionCounter[layerName]++
	version := int64(s.versionCounter[layerName])

	lv := &LayerVersion{
		CompatibleArchitectures: req.CompatibleArchitectures,
		CompatibleRuntimes:      req.CompatibleRuntimes,
		Content: LayerVersionContentOutput{
			CodeSha256: "",
			CodeSize:   int64(len(req.Content.ZipFile)),
			Location:   "",
		},
		CreatedDate:     time.Now().UTC().Format(time.RFC3339),
		Description:     req.Description,
		LayerArn:        s.layerArn(layerName),
		LayerVersionArn: s.layerVersionArn(layerName, version),
		LicenseInfo:     req.LicenseInfo,
		Version:         version,
		LayerName:       layerName,
	}

	s.versions[versionKey(layerName, version)] = lv

	jsonhttp.Write(w, http.StatusCreated, lv)
}
