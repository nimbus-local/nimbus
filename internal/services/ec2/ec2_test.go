package ec2

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func ec2Req(t *testing.T, svc *Service, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	return w
}

func TestDetect(t *testing.T) {
	svc := New()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("Action=DescribeSubnets"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if !svc.Detect(req) {
		t.Fatal("Detect should return true for EC2 POST /")
	}
}

func TestDetectGetMethod(t *testing.T) {
	svc := New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if svc.Detect(req) {
		t.Fatal("Detect should return false for GET requests")
	}
}

func TestDetectJSONContentType(t *testing.T) {
	svc := New()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Content-Type", "application/json")
	if svc.Detect(req) {
		t.Fatal("Detect should return false for JSON content type")
	}
}

func TestDetectNonRootPath(t *testing.T) {
	svc := New()
	req := httptest.NewRequest(http.MethodPost, "/something", strings.NewReader("Action=DescribeSubnets"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if svc.Detect(req) {
		t.Fatal("Detect should return false for non-root path")
	}
}

func TestDescribeSubnetsFilterForm(t *testing.T) {
	svc := New()
	body := "Action=DescribeSubnets&Version=2016-11-15" +
		"&Filter.1.Name=subnet-id&Filter.1.Value.1=subnet-00000001&Filter.1.Value.2=subnet-00000002"
	w := ec2Req(t, svc, body)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	resp := w.Body.String()
	if !strings.Contains(resp, "subnet-00000001") {
		t.Error("response missing subnet-00000001")
	}
	if !strings.Contains(resp, "subnet-00000002") {
		t.Error("response missing subnet-00000002")
	}
	if !strings.Contains(resp, "vpc-00000001") {
		t.Error("response missing vpcId")
	}
	if !strings.Contains(resp, "DescribeSubnetsResponse") {
		t.Error("response missing XML envelope")
	}
}

func TestDescribeSubnetsSubnetIDForm(t *testing.T) {
	svc := New()
	body := "Action=DescribeSubnets&SubnetId.1=subnet-aaa&SubnetId.2=subnet-bbb"
	w := ec2Req(t, svc, body)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	resp := w.Body.String()
	if !strings.Contains(resp, "subnet-aaa") || !strings.Contains(resp, "subnet-bbb") {
		t.Error("response missing requested subnet IDs")
	}
}

func TestUnsupportedAction(t *testing.T) {
	svc := New()
	w := ec2Req(t, svc, "Action=DescribeInstances")
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", w.Code)
	}
}
