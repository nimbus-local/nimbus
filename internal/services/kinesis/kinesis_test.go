package kinesis

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newSvc() *Service { return New("us-east-1") }

// kinesisReq sends a Kinesis_20131202 JSON request.
func kinesisReq(t *testing.T, s *Service, action string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encode: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", &buf)
	req.Header.Set("X-Amz-Target", "Kinesis_20131202."+action)
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	return w
}

// listTags returns the tag map ListTagsForStream reports for a stream.
func listTags(t *testing.T, s *Service, stream string) map[string]string {
	t.Helper()
	w := kinesisReq(t, s, "ListTagsForStream", map[string]interface{}{"StreamName": stream})
	if w.Code != http.StatusOK {
		t.Fatalf("ListTagsForStream: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Tags []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"Tags"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	out := map[string]string{}
	for _, tag := range resp.Tags {
		out[tag.Key] = tag.Value
	}
	return out
}

// CreateStream may carry tags. Dropping them left the Terraform provider
// re-applying the tag set on every plan.
func TestCreateStreamStoresTags(t *testing.T) {
	s := newSvc()
	w := kinesisReq(t, s, "CreateStream", map[string]interface{}{
		"StreamName": "events",
		"ShardCount": 1,
		"Tags":       map[string]string{"Name": "events", "env": "test"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CreateStream: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	tags := listTags(t, s, "events")
	if tags["Name"] != "events" || tags["env"] != "test" {
		t.Errorf("create-time tags did not reach the tag store: %v", tags)
	}
}

// A stream created without tags reports none, and AddTagsToStream still works on
// it — the tag map must not be left nil.
func TestCreateStreamWithoutTags(t *testing.T) {
	s := newSvc()
	kinesisReq(t, s, "CreateStream", map[string]interface{}{
		"StreamName": "plain",
		"ShardCount": 1,
	})
	if tags := listTags(t, s, "plain"); len(tags) != 0 {
		t.Errorf("expected no tags, got %v", tags)
	}

	kinesisReq(t, s, "AddTagsToStream", map[string]interface{}{
		"StreamName": "plain",
		"Tags":       map[string]string{"added": "later"},
	})
	if tags := listTags(t, s, "plain"); tags["added"] != "later" {
		t.Errorf("AddTagsToStream did not apply: %v", tags)
	}
}

// AddTagsToStream must merge into create-time tags rather than replace them.
func TestAddTagsMergesWithCreateTimeTags(t *testing.T) {
	s := newSvc()
	kinesisReq(t, s, "CreateStream", map[string]interface{}{
		"StreamName": "events",
		"ShardCount": 1,
		"Tags":       map[string]string{"Name": "events"},
	})
	kinesisReq(t, s, "AddTagsToStream", map[string]interface{}{
		"StreamName": "events",
		"Tags":       map[string]string{"env": "test"},
	})

	tags := listTags(t, s, "events")
	if tags["Name"] != "events" {
		t.Errorf("create-time tag was lost: %v", tags)
	}
	if tags["env"] != "test" {
		t.Errorf("added tag missing: %v", tags)
	}
}
