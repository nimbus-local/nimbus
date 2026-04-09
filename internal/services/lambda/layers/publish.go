package layers

import (
	"net/http"
	"time"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

type publishRequest struct {
	CompatibleArchitectures []string                 `json:"CompatibleArchitectures,omitempty" validate:"omitempty,dive,oneof=x86_64 arm64"`
	CompatibleRuntimes      []string                 `json:"CompatibleRuntimes,omitempty"`
	Content                 LayerVersionContentInput `json:"Content"`
	Description             string                   `json:"Description,omitempty"`
	LicenseInfo             string                   `json:"LicenseInfo,omitempty"`
}

// POST /2015-03-31/layers/{LayerName}/versions
func (s *Service) Publish(w http.ResponseWriter, r *http.Request, layerName string) {
	req, ok := jsonhttp.DecodeAndValidate[publishRequest](w, r)
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
