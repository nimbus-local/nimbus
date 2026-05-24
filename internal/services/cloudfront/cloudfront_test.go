package cloudfront

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newSvc() *Service { return New("us-east-1") }

const minimalConfig = `<?xml version="1.0" encoding="UTF-8"?>
<DistributionConfig>
  <CallerReference>ref-1</CallerReference>
  <Enabled>true</Enabled>
  <Comment>test dist</Comment>
  <Origins>
    <Quantity>1</Quantity>
    <Items>
      <Origin>
        <Id>origin-1</Id>
        <DomainName>example.com</DomainName>
      </Origin>
    </Items>
  </Origins>
  <DefaultCacheBehavior>
    <ViewerProtocolPolicy>allow-all</ViewerProtocolPolicy>
    <TargetOriginId>origin-1</TargetOriginId>
    <ForwardedValues>
      <QueryString>false</QueryString>
      <Cookies><Forward>none</Forward></Cookies>
    </ForwardedValues>
    <MinTTL>0</MinTTL>
  </DefaultCacheBehavior>
</DistributionConfig>`

func createDist(t *testing.T, s *Service) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/2020-05-31/distribution", strings.NewReader(minimalConfig))
	req.Header.Set("Content-Type", "text/xml")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("createDist: expected 201, got %d\n%s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	parts := strings.Split(loc, "/")
	return parts[len(parts)-1]
}

func doReq(t *testing.T, s *Service, method, path string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	return w
}

// --- Detect ---

func TestDetect(t *testing.T) {
	s := newSvc()
	req := httptest.NewRequest(http.MethodGet, "/2020-05-31/distribution", nil)
	if !s.Detect(req) {
		t.Fatal("expected Detect=true for /2020-05-31/ path")
	}
}

func TestDetect_Miss(t *testing.T) {
	s := newSvc()
	req := httptest.NewRequest(http.MethodGet, "/other", nil)
	if s.Detect(req) {
		t.Fatal("expected Detect=false")
	}
}

// --- CreateDistribution ---

func TestCreateDistribution(t *testing.T) {
	s := newSvc()
	req := httptest.NewRequest(http.MethodPost, "/2020-05-31/distribution", strings.NewReader(minimalConfig))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d\n%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Location"), "/2020-05-31/distribution/") {
		t.Error("expected Location header with distribution path")
	}
	if w.Header().Get("ETag") == "" {
		t.Error("expected ETag header")
	}
	if !strings.Contains(w.Body.String(), "Deployed") {
		t.Error("expected Status=Deployed in response body")
	}
}

// --- GetDistribution ---

func TestGetDistribution(t *testing.T) {
	s := newSvc()
	id := createDist(t, s)

	w := doReq(t, s, http.MethodGet, "/2020-05-31/distribution/"+id, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), id) {
		t.Error("expected distribution ID in response")
	}
}

func TestGetDistribution_NotFound(t *testing.T) {
	s := newSvc()
	w := doReq(t, s, http.MethodGet, "/2020-05-31/distribution/DOESNOTEXIST", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "NoSuchDistribution") {
		t.Error("expected NoSuchDistribution error code")
	}
}

// --- UpdateDistribution ---

func TestUpdateDistribution(t *testing.T) {
	s := newSvc()
	id := createDist(t, s)

	updated := strings.ReplaceAll(minimalConfig, "test dist", "updated dist")
	w := doReq(t, s, http.MethodPut, fmt.Sprintf("/2020-05-31/distribution/%s/config", id), strings.NewReader(updated))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}
}

func TestUpdateDistribution_NotFound(t *testing.T) {
	s := newSvc()
	w := doReq(t, s, http.MethodPut, "/2020-05-31/distribution/NOPE/config", strings.NewReader(minimalConfig))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// --- DeleteDistribution ---

func TestDeleteDistribution(t *testing.T) {
	s := newSvc()
	id := createDist(t, s)

	w := doReq(t, s, http.MethodDelete, "/2020-05-31/distribution/"+id, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
	// Confirm gone
	w2 := doReq(t, s, http.MethodGet, "/2020-05-31/distribution/"+id, nil)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", w2.Code)
	}
}

func TestDeleteDistribution_NotFound(t *testing.T) {
	s := newSvc()
	w := doReq(t, s, http.MethodDelete, "/2020-05-31/distribution/NOPE", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// --- ListDistributions ---

func TestListDistributions_Empty(t *testing.T) {
	s := newSvc()
	w := doReq(t, s, http.MethodGet, "/2020-05-31/distribution", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "DistributionList") {
		t.Error("expected DistributionList in response")
	}
}

func TestListDistributions_AfterCreate(t *testing.T) {
	s := newSvc()
	createDist(t, s)
	createDist(t, s)

	w := doReq(t, s, http.MethodGet, "/2020-05-31/distribution", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "DistributionSummary") {
		t.Error("expected DistributionSummary items")
	}
}

// --- Tags ---

func TestListTagsForResource(t *testing.T) {
	s := newSvc()
	w := doReq(t, s, http.MethodGet, "/2020-05-31/tagging?Resource=arn:aws:cloudfront::000000000000:distribution/ABCD", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Tagging") {
		t.Error("expected Tagging in response")
	}
}

func TestAddTagsToResource(t *testing.T) {
	s := newSvc()
	w := doReq(t, s, http.MethodPost, "/2020-05-31/tagging?Resource=arn:aws:cloudfront::000000000000:distribution/ABCD",
		strings.NewReader(`<Tags><Items><Tag><Key>env</Key><Value>dev</Value></Tag></Items></Tags>`))
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
}

// --- Unknown path ---

func TestUnknownPath(t *testing.T) {
	s := newSvc()
	w := doReq(t, s, http.MethodGet, "/2020-05-31/unknown/path", nil)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", w.Code)
	}
}

// --- DistributionsHandler (inspection endpoint) ---

func TestDistributionsHandler(t *testing.T) {
	s := newSvc()
	createDist(t, s)

	req := httptest.NewRequest(http.MethodGet, "/_nimbus/cloudfront/distributions", nil)
	w := httptest.NewRecorder()
	s.DistributionsHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "cloudfront.localhost") {
		t.Error("expected cloudfront.localhost domain in inspection response")
	}
}

// --- normalizeDistConfig ---

func TestNormalizeDistConfig_WithTags(t *testing.T) {
	wrapped := `<DistributionConfigWithTags>
		<DistributionConfig><Enabled>true</Enabled></DistributionConfig>
		<Tags><Items/></Tags>
	</DistributionConfigWithTags>`
	result := normalizeDistConfig([]byte(wrapped))
	if !strings.Contains(string(result), "<DistributionConfig>") {
		t.Error("expected <DistributionConfig> in normalized output")
	}
	if strings.Contains(string(result), "WithTags") {
		t.Error("expected WithTags wrapper to be stripped")
	}
}

func TestNormalizeDistConfig_Plain(t *testing.T) {
	plain := `<DistributionConfig><Enabled>true</Enabled></DistributionConfig>`
	result := normalizeDistConfig([]byte(plain))
	if string(result) != plain {
		t.Errorf("expected plain config unchanged, got %s", result)
	}
}
