package elasticache

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func newSvc() *Service {
	return New("us-east-1", "localhost", 6379)
}

func ecReq(t *testing.T, svc *Service, action string, extra url.Values) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"Action": {action}, "Version": {ecAPIVersion}}
	for k, vs := range extra {
		form[k] = vs
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	return w
}

// assertTag verifies ListTagsForResource returns the expected key/value.
func assertTag(t *testing.T, svc *Service, arn, key, want string) {
	t.Helper()
	w := ecReq(t, svc, "ListTagsForResource", url.Values{"ResourceName": {arn}})
	if w.Code != http.StatusOK {
		t.Fatalf("ListTagsForResource: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, fmt.Sprintf("<Key>%s</Key>", key)) {
		t.Errorf("expected tag key %q in ListTagsForResource response:\n%s", key, body)
	}
	if want != "" && !strings.Contains(body, fmt.Sprintf("<Value>%s</Value>", want)) {
		t.Errorf("expected tag value %q in ListTagsForResource response:\n%s", want, body)
	}
}

// tagValues builds url.Values for tags using ElastiCache's query protocol format.
// ElastiCache uses locationName "Tag" for list members: Tags.Tag.N.Key / Tags.Tag.N.Value.
func tagValues(tags map[string]string) url.Values {
	v := url.Values{}
	i := 1
	for k, val := range tags {
		v.Set(fmt.Sprintf("Tags.Tag.%d.Key", i), k)
		v.Set(fmt.Sprintf("Tags.Tag.%d.Value", i), val)
		i++
	}
	return v
}

// ── Tags: AddTagsToResource ───────────────────────────────────────────────────

func TestAddTagsToResource_ResponseShape(t *testing.T) {
	svc := newSvc()
	arn := "arn:aws:elasticache:us-east-1:000000000000:subnetgroup:sg1"
	w := ecReq(t, svc, "AddTagsToResource", url.Values{
		"ResourceName":      {arn},
		"Tags.Tag.1.Key":   {"env"},
		"Tags.Tag.1.Value": {"dev"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	// Pulumi v7 parser requires AddTagsToResourceResult; TagList causes a deserialization error.
	if !strings.Contains(body, "AddTagsToResourceResult") {
		t.Errorf("response missing AddTagsToResourceResult node:\n%s", body)
	}
	if strings.Contains(body, "TagList") {
		t.Errorf("response must not contain TagList (Pulumi v7 rejects it):\n%s", body)
	}
}

func TestAddTagsToResource_StoredAndRetrievable(t *testing.T) {
	svc := newSvc()
	arn := "arn:aws:elasticache:us-east-1:000000000000:subnetgroup:sg1"
	ecReq(t, svc, "AddTagsToResource", url.Values{
		"ResourceName":      {arn},
		"Tags.Tag.1.Key":   {"env"},
		"Tags.Tag.1.Value": {"staging"},
	})
	assertTag(t, svc, arn, "env", "staging")
}

func TestAddTagsToResource_MultipleTagsStored(t *testing.T) {
	svc := newSvc()
	arn := "arn:aws:elasticache:us-east-1:000000000000:replicationgroup:rg1"
	ecReq(t, svc, "AddTagsToResource", url.Values{
		"ResourceName":      {arn},
		"Tags.Tag.1.Key":   {"app"},
		"Tags.Tag.1.Value": {"myapp"},
		"Tags.Tag.2.Key":   {"env"},
		"Tags.Tag.2.Value": {"prod"},
	})
	assertTag(t, svc, arn, "app", "myapp")
	assertTag(t, svc, arn, "env", "prod")
}

// ── Tags: ListTagsForResource ─────────────────────────────────────────────────

func TestListTagsForResource_Empty(t *testing.T) {
	svc := newSvc()
	arn := "arn:aws:elasticache:us-east-1:000000000000:subnetgroup:unknown"
	w := ecReq(t, svc, "ListTagsForResource", url.Values{"ResourceName": {arn}})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "ListTagsForResourceResult") {
		t.Errorf("response missing ListTagsForResourceResult:\n%s", w.Body.String())
	}
}

// ── Tags: RemoveTagsFromResource ──────────────────────────────────────────────

func TestRemoveTagsFromResource_ResponseShape(t *testing.T) {
	svc := newSvc()
	arn := "arn:aws:elasticache:us-east-1:000000000000:subnetgroup:sg1"
	w := ecReq(t, svc, "RemoveTagsFromResource", url.Values{
		"ResourceName":     {arn},
		"TagKeys.member.1": {"env"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "RemoveTagsFromResourceResult") {
		t.Errorf("response missing RemoveTagsFromResourceResult:\n%s", w.Body.String())
	}
}

func TestRemoveTagsFromResource(t *testing.T) {
	svc := newSvc()
	arn := "arn:aws:elasticache:us-east-1:000000000000:subnetgroup:sg1"
	ecReq(t, svc, "AddTagsToResource", url.Values{
		"ResourceName":      {arn},
		"Tags.Tag.1.Key":   {"env"},
		"Tags.Tag.1.Value": {"dev"},
		"Tags.Tag.2.Key":   {"app"},
		"Tags.Tag.2.Value": {"myapp"},
	})

	w := ecReq(t, svc, "RemoveTagsFromResource", url.Values{
		"ResourceName":     {arn},
		"TagKeys.member.1": {"env"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}

	lw := ecReq(t, svc, "ListTagsForResource", url.Values{"ResourceName": {arn}})
	body := lw.Body.String()
	if strings.Contains(body, "<Key>env</Key>") {
		t.Errorf("env tag should have been removed:\n%s", body)
	}
	if !strings.Contains(body, "<Key>app</Key>") {
		t.Errorf("app tag should still be present:\n%s", body)
	}
}

// ── Tags stored at create time ────────────────────────────────────────────────

func TestCreateSubnetGroup_TagsStored(t *testing.T) {
	svc := newSvc()
	w := ecReq(t, svc, "CreateCacheSubnetGroup", url.Values{
		"CacheSubnetGroupName":        {"my-sg"},
		"CacheSubnetGroupDescription": {"test"},
		"Tags.Tag.1.Key":             {"forge:app"},
		"Tags.Tag.1.Value":           {"myapp"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CreateCacheSubnetGroup: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	assertTag(t, svc, svc.subnetGroupARN("my-sg"), "forge:app", "myapp")
}

func TestCreateParameterGroup_TagsStored(t *testing.T) {
	svc := newSvc()
	w := ecReq(t, svc, "CreateCacheParameterGroup", url.Values{
		"CacheParameterGroupName":   {"my-pg"},
		"CacheParameterGroupFamily": {"valkey7"},
		"Description":               {"test"},
		"Tags.Tag.1.Key":           {"forge:stage"},
		"Tags.Tag.1.Value":         {"ci"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CreateCacheParameterGroup: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	assertTag(t, svc, svc.paramGroupARN("my-pg"), "forge:stage", "ci")
}

func TestCreateReplicationGroup_TagsStored(t *testing.T) {
	svc := newSvc()
	w := ecReq(t, svc, "CreateReplicationGroup", url.Values{
		"ReplicationGroupId":          {"my-rg"},
		"ReplicationGroupDescription": {"test"},
		"Tags.Tag.1.Key":             {"forge:name"},
		"Tags.Tag.1.Value":           {"Cache"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CreateReplicationGroup: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	assertTag(t, svc, svc.replGroupARN("my-rg"), "forge:name", "Cache")
}

func TestCreateCacheCluster_TagsStored(t *testing.T) {
	svc := newSvc()
	w := ecReq(t, svc, "CreateCacheCluster", url.Values{
		"CacheClusterId":    {"my-cluster"},
		"Engine":            {"valkey"},
		"CacheNodeType":     {"cache.t3.micro"},
		"Tags.Tag.1.Key":   {"forge:app"},
		"Tags.Tag.1.Value": {"smoke"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CreateCacheCluster: expected 200, got %d\n%s", w.Code, w.Body.String())
	}
	assertTag(t, svc, svc.clusterARN("my-cluster"), "forge:app", "smoke")
}
