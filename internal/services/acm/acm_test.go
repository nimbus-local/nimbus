package acm

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newSvc() *Service { return New("us-east-1") }

func acmReq(t *testing.T, s *Service, action string, body map[string]interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	req := httptest.NewRequest(http.MethodPost, "/", &buf)
	req.Header.Set("X-Amz-Target", "CertificateManager."+action)
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	return w
}

func requestCert(t *testing.T, s *Service, domain string) string {
	t.Helper()
	w := acmReq(t, s, "RequestCertificate", map[string]interface{}{
		"DomainName": domain,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("RequestCertificate: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	arn := resp["CertificateArn"]
	if arn == "" {
		t.Fatal("expected non-empty CertificateArn")
	}
	return arn
}

// --- Detect ---

func TestDetect(t *testing.T) {
	s := newSvc()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Amz-Target", "CertificateManager.RequestCertificate")
	if !s.Detect(req) {
		t.Fatal("expected Detect=true")
	}
}

func TestDetect_Miss(t *testing.T) {
	s := newSvc()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Amz-Target", "AmazonS3.PutObject")
	if s.Detect(req) {
		t.Fatal("expected Detect=false")
	}
}

// --- RequestCertificate ---

func TestRequestCertificate(t *testing.T) {
	s := newSvc()
	arn := requestCert(t, s, "example.com")
	if !strings.Contains(arn, "arn:aws:acm:us-east-1:") {
		t.Errorf("unexpected ARN format: %s", arn)
	}
}

func TestRequestCertificate_WithSANs(t *testing.T) {
	s := newSvc()
	w := acmReq(t, s, "RequestCertificate", map[string]interface{}{
		"DomainName":              "example.com",
		"SubjectAlternativeNames": []string{"www.example.com", "api.example.com"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestRequestCertificate_MissingDomain(t *testing.T) {
	s := newSvc()
	w := acmReq(t, s, "RequestCertificate", map[string]interface{}{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- DescribeCertificate ---

func TestDescribeCertificate(t *testing.T) {
	s := newSvc()
	arn := requestCert(t, s, "example.com")

	w := acmReq(t, s, "DescribeCertificate", map[string]interface{}{"CertificateArn": arn})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	cert, _ := resp["Certificate"].(map[string]interface{})
	if cert["Status"] != "ISSUED" {
		t.Errorf("expected Status=ISSUED, got %v", cert["Status"])
	}
	if cert["DomainName"] != "example.com" {
		t.Errorf("expected DomainName=example.com, got %v", cert["DomainName"])
	}
}

func TestDescribeCertificate_NotFound(t *testing.T) {
	s := newSvc()
	w := acmReq(t, s, "DescribeCertificate", map[string]interface{}{"CertificateArn": "arn:aws:acm:us-east-1:000000000000:certificate/nope"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- GetCertificate ---

func TestGetCertificate(t *testing.T) {
	s := newSvc()
	arn := requestCert(t, s, "example.com")

	w := acmReq(t, s, "GetCertificate", map[string]interface{}{"CertificateArn": arn})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if !strings.HasPrefix(resp["Certificate"], "-----BEGIN CERTIFICATE-----") {
		t.Error("expected PEM certificate in Certificate field")
	}
}

func TestGetCertificate_NotFound(t *testing.T) {
	s := newSvc()
	w := acmReq(t, s, "GetCertificate", map[string]interface{}{"CertificateArn": "arn:nope"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- ListCertificates ---

func TestListCertificates(t *testing.T) {
	s := newSvc()
	requestCert(t, s, "a.com")
	requestCert(t, s, "b.com")

	w := acmReq(t, s, "ListCertificates", map[string]interface{}{})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	list, _ := resp["CertificateSummaryList"].([]interface{})
	if len(list) < 2 {
		t.Errorf("expected at least 2 certificates, got %d", len(list))
	}
}

// --- DeleteCertificate ---

func TestDeleteCertificate(t *testing.T) {
	s := newSvc()
	arn := requestCert(t, s, "example.com")

	w := acmReq(t, s, "DeleteCertificate", map[string]interface{}{"CertificateArn": arn})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	// Confirm gone
	w2 := acmReq(t, s, "DescribeCertificate", map[string]interface{}{"CertificateArn": arn})
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 after delete, got %d", w2.Code)
	}
}

func TestDeleteCertificate_NotFound(t *testing.T) {
	s := newSvc()
	w := acmReq(t, s, "DeleteCertificate", map[string]interface{}{"CertificateArn": "arn:nope"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- Tags ---

func TestTagsRoundTrip(t *testing.T) {
	s := newSvc()
	arn := requestCert(t, s, "example.com")

	acmReq(t, s, "AddTagsToCertificate", map[string]interface{}{
		"CertificateArn": arn,
		"Tags":           []map[string]string{{"Key": "env", "Value": "dev"}},
	})

	w := acmReq(t, s, "ListTagsForCertificate", map[string]interface{}{"CertificateArn": arn})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "env") {
		t.Error("expected tag key 'env' in response")
	}
}

func TestRemoveTags(t *testing.T) {
	s := newSvc()
	arn := requestCert(t, s, "example.com")

	acmReq(t, s, "AddTagsToCertificate", map[string]interface{}{
		"CertificateArn": arn,
		"Tags":           []map[string]string{{"Key": "tmp", "Value": "yes"}},
	})
	acmReq(t, s, "RemoveTagsFromCertificate", map[string]interface{}{
		"CertificateArn": arn,
		"Tags":           []map[string]string{{"Key": "tmp", "Value": "yes"}},
	})

	w := acmReq(t, s, "ListTagsForCertificate", map[string]interface{}{"CertificateArn": arn})
	if strings.Contains(w.Body.String(), "tmp") {
		t.Error("expected removed tag to be absent")
	}
}

func TestAddTags_CertNotFound(t *testing.T) {
	s := newSvc()
	w := acmReq(t, s, "AddTagsToCertificate", map[string]interface{}{
		"CertificateArn": "arn:nope",
		"Tags":           []map[string]string{{"Key": "k", "Value": "v"}},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- Stub operations ---

func TestRenewCertificate(t *testing.T) {
	s := newSvc()
	arn := requestCert(t, s, "example.com")
	w := acmReq(t, s, "RenewCertificate", map[string]interface{}{"CertificateArn": arn})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestExportCertificate(t *testing.T) {
	s := newSvc()
	arn := requestCert(t, s, "example.com")
	w := acmReq(t, s, "ExportCertificate", map[string]interface{}{"CertificateArn": arn})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestUnknownAction(t *testing.T) {
	s := newSvc()
	w := acmReq(t, s, "UnknownAction", map[string]interface{}{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- CertHandler ---

func TestCertHandler(t *testing.T) {
	s := newSvc()
	arn := requestCert(t, s, "example.com")
	// Extract the UUID at the end of the ARN
	parts := strings.Split(arn, "/")
	certID := parts[len(parts)-1]

	req := httptest.NewRequest(http.MethodGet, "/_nimbus/acm/certs/"+certID, nil)
	w := httptest.NewRecorder()
	s.CertHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("CertHandler: expected 200, got %d", w.Code)
	}
	if !strings.HasPrefix(w.Body.String(), "-----BEGIN CERTIFICATE-----") {
		t.Error("expected PEM certificate from CertHandler")
	}
}

func TestCertHandler_NotFound(t *testing.T) {
	s := newSvc()
	req := httptest.NewRequest(http.MethodGet, "/_nimbus/acm/certs/nonexistent-id", nil)
	w := httptest.NewRecorder()
	s.CertHandler(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// --- RequestCertificate stores tags ---

func TestRequestCertificate_WithTags(t *testing.T) {
	s := newSvc()
	w := acmReq(t, s, "RequestCertificate", map[string]interface{}{
		"DomainName": "tagged.com",
		"Tags":       []map[string]string{{"Key": "project", "Value": "nimbus"}},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	arn := resp["CertificateArn"]

	wt := acmReq(t, s, "ListTagsForCertificate", map[string]interface{}{"CertificateArn": arn})
	if !strings.Contains(wt.Body.String(), "project") {
		t.Error("expected tag stored during RequestCertificate")
	}
}
