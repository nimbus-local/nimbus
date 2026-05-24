package route53

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newSvc() *Service { return New() }

func doReq(t *testing.T, s *Service, method, path string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	return w
}

const createZoneBody = `<?xml version="1.0" encoding="UTF-8"?>
<CreateHostedZoneRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <Name>example.com</Name>
  <CallerReference>ref-1</CallerReference>
  <HostedZoneConfig>
    <Comment>test zone</Comment>
    <PrivateZone>false</PrivateZone>
  </HostedZoneConfig>
</CreateHostedZoneRequest>`

func createZone(t *testing.T, s *Service) string {
	t.Helper()
	w := doReq(t, s, http.MethodPost, "/2013-04-01/hostedzone", strings.NewReader(createZoneBody))
	if w.Code != http.StatusCreated {
		t.Fatalf("createZone: expected 201, got %d\n%s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	parts := strings.Split(strings.TrimRight(loc, "/"), "/")
	return parts[len(parts)-1]
}

// --- Detect ---

func TestDetect(t *testing.T) {
	s := newSvc()
	req := httptest.NewRequest(http.MethodGet, "/2013-04-01/hostedzone", nil)
	if !s.Detect(req) {
		t.Fatal("expected Detect=true")
	}
}

func TestDetect_Miss(t *testing.T) {
	s := newSvc()
	req := httptest.NewRequest(http.MethodGet, "/other", nil)
	if s.Detect(req) {
		t.Fatal("expected Detect=false")
	}
}

// --- CreateHostedZone ---

func TestCreateHostedZone(t *testing.T) {
	s := newSvc()
	w := doReq(t, s, http.MethodPost, "/2013-04-01/hostedzone", strings.NewReader(createZoneBody))
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d\n%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "example.com") {
		t.Error("expected zone name in response")
	}
	if !strings.Contains(body, "INSYNC") {
		t.Error("expected INSYNC change status")
	}
	if w.Header().Get("Location") == "" {
		t.Error("expected Location header")
	}
}

// --- GetHostedZone ---

func TestGetHostedZone(t *testing.T) {
	s := newSvc()
	id := createZone(t, s)

	w := doReq(t, s, http.MethodGet, "/2013-04-01/hostedzone/"+id, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), id) {
		t.Error("expected zone ID in response")
	}
}

func TestGetHostedZone_NotFound(t *testing.T) {
	s := newSvc()
	w := doReq(t, s, http.MethodGet, "/2013-04-01/hostedzone/ZNOTEXIST", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "NoSuchHostedZone") {
		t.Error("expected NoSuchHostedZone error")
	}
}

// --- ListHostedZones ---

func TestListHostedZones(t *testing.T) {
	s := newSvc()
	createZone(t, s)
	createZone(t, s)

	w := doReq(t, s, http.MethodGet, "/2013-04-01/hostedzone", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "ListHostedZonesResponse") {
		t.Error("expected ListHostedZonesResponse")
	}
	if strings.Count(w.Body.String(), "<HostedZone>") < 2 {
		t.Error("expected at least 2 zones in response")
	}
}

// --- DeleteHostedZone ---

func TestDeleteHostedZone(t *testing.T) {
	s := newSvc()
	id := createZone(t, s)

	w := doReq(t, s, http.MethodDelete, "/2013-04-01/hostedzone/"+id, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	// Confirm gone
	w2 := doReq(t, s, http.MethodGet, "/2013-04-01/hostedzone/"+id, nil)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", w2.Code)
	}
}

func TestDeleteHostedZone_NotFound(t *testing.T) {
	s := newSvc()
	w := doReq(t, s, http.MethodDelete, "/2013-04-01/hostedzone/ZNOTEXIST", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// --- GetChange ---

func TestGetChange(t *testing.T) {
	s := newSvc()
	w := doReq(t, s, http.MethodGet, "/2013-04-01/change/C1234567890", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "INSYNC") {
		t.Error("expected INSYNC status")
	}
}

// --- GetHostedZoneCount ---

func TestGetHostedZoneCount(t *testing.T) {
	s := newSvc()
	createZone(t, s)

	w := doReq(t, s, http.MethodGet, "/2013-04-01/hostedzonecount", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "HostedZoneCount") {
		t.Error("expected HostedZoneCount in response")
	}
}

// --- ChangeResourceRecordSets ---

func rrsetBody(action, name, rrType, value string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<ChangeResourceRecordSetsRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <ChangeBatch>
    <Changes>
      <Change>
        <Action>%s</Action>
        <ResourceRecordSet>
          <Name>%s</Name>
          <Type>%s</Type>
          <TTL>300</TTL>
          <ResourceRecords>
            <ResourceRecord><Value>%s</Value></ResourceRecord>
          </ResourceRecords>
        </ResourceRecordSet>
      </Change>
    </Changes>
  </ChangeBatch>
</ChangeResourceRecordSetsRequest>`, action, name, rrType, value)
}

func TestChangeResourceRecordSets_CreateAndList(t *testing.T) {
	s := newSvc()
	id := createZone(t, s)

	w := doReq(t, s, http.MethodPost,
		"/2013-04-01/hostedzone/"+id+"/rrset",
		strings.NewReader(rrsetBody("CREATE", "www.example.com", "A", "1.2.3.4")))
	if w.Code != http.StatusOK {
		t.Fatalf("ChangeRRSets: expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	w2 := doReq(t, s, http.MethodGet, "/2013-04-01/hostedzone/"+id+"/rrset", nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("ListRRSets: expected 200, got %d", w2.Code)
	}
	if !strings.Contains(w2.Body.String(), "1.2.3.4") {
		t.Error("expected record value in list response")
	}
}

func TestChangeResourceRecordSets_Upsert(t *testing.T) {
	s := newSvc()
	id := createZone(t, s)

	doReq(t, s, http.MethodPost, "/2013-04-01/hostedzone/"+id+"/rrset",
		strings.NewReader(rrsetBody("CREATE", "api.example.com", "A", "10.0.0.1")))
	doReq(t, s, http.MethodPost, "/2013-04-01/hostedzone/"+id+"/rrset",
		strings.NewReader(rrsetBody("UPSERT", "api.example.com", "A", "10.0.0.2")))

	w := doReq(t, s, http.MethodGet, "/2013-04-01/hostedzone/"+id+"/rrset", nil)
	if !strings.Contains(w.Body.String(), "10.0.0.2") {
		t.Error("expected upserted value 10.0.0.2")
	}
}

func TestChangeResourceRecordSets_Delete(t *testing.T) {
	s := newSvc()
	id := createZone(t, s)

	doReq(t, s, http.MethodPost, "/2013-04-01/hostedzone/"+id+"/rrset",
		strings.NewReader(rrsetBody("CREATE", "del.example.com", "A", "5.5.5.5")))
	doReq(t, s, http.MethodPost, "/2013-04-01/hostedzone/"+id+"/rrset",
		strings.NewReader(rrsetBody("DELETE", "del.example.com", "A", "5.5.5.5")))

	w := doReq(t, s, http.MethodGet, "/2013-04-01/hostedzone/"+id+"/rrset", nil)
	if strings.Contains(w.Body.String(), "5.5.5.5") {
		t.Error("expected deleted record to be absent")
	}
}

func TestChangeResourceRecordSets_ZoneNotFound(t *testing.T) {
	s := newSvc()
	w := doReq(t, s, http.MethodPost, "/2013-04-01/hostedzone/ZNOPE/rrset",
		strings.NewReader(rrsetBody("CREATE", "x.example.com", "A", "1.1.1.1")))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestListResourceRecordSets_ZoneNotFound(t *testing.T) {
	s := newSvc()
	w := doReq(t, s, http.MethodGet, "/2013-04-01/hostedzone/ZNOPE/rrset", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// --- Tags ---

func TestTagsRoundTrip(t *testing.T) {
	s := newSvc()
	id := createZone(t, s)

	// Add tags
	addBody := `<ChangeTagsForResourceRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
	  <AddTags><Tag><Key>env</Key><Value>dev</Value></Tag></AddTags>
	</ChangeTagsForResourceRequest>`
	w := doReq(t, s, http.MethodPost, "/2013-04-01/tags/hostedzone/"+id, strings.NewReader(addBody))
	if w.Code != http.StatusOK {
		t.Fatalf("AddTags: expected 200, got %d", w.Code)
	}

	// List tags
	w2 := doReq(t, s, http.MethodGet, "/2013-04-01/tags/hostedzone/"+id, nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("ListTags: expected 200, got %d", w2.Code)
	}
	if !strings.Contains(w2.Body.String(), "env") {
		t.Error("expected tag key 'env' in response")
	}
}

func TestTagsRemove(t *testing.T) {
	s := newSvc()
	id := createZone(t, s)

	addBody := `<ChangeTagsForResourceRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
	  <AddTags><Tag><Key>tmp</Key><Value>yes</Value></Tag></AddTags>
	</ChangeTagsForResourceRequest>`
	doReq(t, s, http.MethodPost, "/2013-04-01/tags/hostedzone/"+id, strings.NewReader(addBody))

	removeBody := `<ChangeTagsForResourceRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
	  <RemoveTagKeys><Key>tmp</Key></RemoveTagKeys>
	</ChangeTagsForResourceRequest>`
	doReq(t, s, http.MethodPost, "/2013-04-01/tags/hostedzone/"+id, strings.NewReader(removeBody))

	w := doReq(t, s, http.MethodGet, "/2013-04-01/tags/hostedzone/"+id, nil)
	if strings.Contains(w.Body.String(), "tmp") {
		t.Error("expected removed tag to be absent")
	}
}

// --- Unknown path ---

func TestUnknownPath(t *testing.T) {
	s := newSvc()
	w := doReq(t, s, http.MethodGet, "/2013-04-01/unknownresource", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- Method not allowed on known paths ---

func TestHostedZoneMethodNotAllowed(t *testing.T) {
	s := newSvc()
	id := createZone(t, s)
	w := doReq(t, s, http.MethodPut, "/2013-04-01/hostedzone/"+id, nil)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}
