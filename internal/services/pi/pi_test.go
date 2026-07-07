package pi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func call(t *testing.T, s *Service, action, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", targetPrefix+action)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON response: %v\n%s", err, rec.Body.String())
	}
	return rec.Code, out
}

func TestGetResourceMetrics(t *testing.T) {
	s := New(nil)
	code, out := call(t, s, "GetResourceMetrics", `{
		"ServiceType": "RDS",
		"Identifier": "db-TESTRESOURCEID",
		"MetricQueries": [{"Metric": "db.load.avg"}],
		"StartTime": 1750000200,
		"EndTime": 1750003800,
		"PeriodInSeconds": 300
	}`)
	if code != 200 {
		t.Fatalf("status = %d, want 200: %v", code, out)
	}
	if out["Identifier"] != "db-TESTRESOURCEID" {
		t.Fatalf("Identifier = %v", out["Identifier"])
	}
	list := out["MetricList"].([]any)
	if len(list) != 1 {
		t.Fatalf("MetricList length = %d, want 1", len(list))
	}
	series := list[0].(map[string]any)
	if series["Key"].(map[string]any)["Metric"] != "db.load.avg" {
		t.Fatalf("Key.Metric = %v", series["Key"])
	}
	points := series["DataPoints"].([]any)
	if len(points) != 12 { // 3600s window / 300s period
		t.Fatalf("DataPoints length = %d, want 12", len(points))
	}
	p0 := points[0].(map[string]any)
	if p0["Timestamp"].(float64) != 1750000500 {
		t.Fatalf("first Timestamp = %v, want 1750000500", p0["Timestamp"])
	}
	v := p0["Value"].(float64)
	if v < 0 || v > 1 {
		t.Fatalf("Value = %v, want within [0, 1]", v)
	}
	if out["AlignedStartTime"].(float64) != 1750000200 {
		t.Fatalf("AlignedStartTime = %v, want 1750000200", out["AlignedStartTime"])
	}
}

func TestGetResourceMetrics_GroupBy(t *testing.T) {
	s := New(nil)
	code, out := call(t, s, "GetResourceMetrics", `{
		"ServiceType": "RDS",
		"Identifier": "db-TESTRESOURCEID",
		"MetricQueries": [{"Metric": "db.load.avg", "GroupBy": {"Group": "db.wait_event", "Limit": 2}}],
		"StartTime": 1750000000,
		"EndTime": 1750000600,
		"PeriodInSeconds": 60
	}`)
	if code != 200 {
		t.Fatalf("status = %d: %v", code, out)
	}
	list := out["MetricList"].([]any)
	if len(list) != 2 { // Limit 2 out of 3 wait events
		t.Fatalf("MetricList length = %d, want 2", len(list))
	}
	dims := list[0].(map[string]any)["Key"].(map[string]any)["Dimensions"].(map[string]any)
	if dims["db.wait_event.name"] != "CPU" {
		t.Fatalf("first group dimensions = %v", dims)
	}
}

func TestGetResourceMetrics_UnknownIdentifier(t *testing.T) {
	s := New(func(id string) bool { return id == "db-KNOWN" })
	code, out := call(t, s, "GetResourceMetrics", `{
		"ServiceType": "RDS",
		"Identifier": "db-UNKNOWN",
		"MetricQueries": [{"Metric": "db.load.avg"}],
		"StartTime": 1750000000,
		"EndTime": 1750003600
	}`)
	if code != 400 {
		t.Fatalf("status = %d, want 400", code)
	}
	if out["__type"] != "InvalidArgumentException" {
		t.Fatalf("__type = %v, want InvalidArgumentException", out["__type"])
	}
}

func TestDescribeDimensionKeys(t *testing.T) {
	s := New(nil)
	code, out := call(t, s, "DescribeDimensionKeys", `{
		"ServiceType": "RDS",
		"Identifier": "db-TESTRESOURCEID",
		"Metric": "db.load.avg",
		"GroupBy": {"Group": "db.wait_event"},
		"StartTime": 1750000000,
		"EndTime": 1750003600,
		"PeriodInSeconds": 300
	}`)
	if code != 200 {
		t.Fatalf("status = %d: %v", code, out)
	}
	keys := out["Keys"].([]any)
	if len(keys) != 3 {
		t.Fatalf("Keys length = %d, want 3", len(keys))
	}
	first := keys[0].(map[string]any)
	if first["Dimensions"].(map[string]any)["db.wait_event.name"] != "CPU" {
		t.Fatalf("first key = %v", first)
	}
	if first["Total"].(float64) <= 0 {
		t.Fatalf("Total = %v, want > 0", first["Total"])
	}
}

func TestListAvailableResourceMetrics_FiltersByType(t *testing.T) {
	s := New(nil)
	code, out := call(t, s, "ListAvailableResourceMetrics", `{
		"ServiceType": "RDS",
		"Identifier": "db-TESTRESOURCEID",
		"MetricTypes": ["db"]
	}`)
	if code != 200 {
		t.Fatalf("status = %d: %v", code, out)
	}
	metrics := out["Metrics"].([]any)
	if len(metrics) != 2 {
		t.Fatalf("Metrics length = %d, want 2 (db.* only)", len(metrics))
	}
	for _, m := range metrics {
		name := m.(map[string]any)["Metric"].(string)
		if !strings.HasPrefix(name, "db.") {
			t.Fatalf("unexpected metric %q for type filter db", name)
		}
	}
}

func TestGetResourceMetadata(t *testing.T) {
	s := New(nil)
	code, out := call(t, s, "GetResourceMetadata", `{
		"ServiceType": "RDS",
		"Identifier": "db-TESTRESOURCEID"
	}`)
	if code != 200 {
		t.Fatalf("status = %d: %v", code, out)
	}
	if out["Identifier"] != "db-TESTRESOURCEID" {
		t.Fatalf("Identifier = %v", out["Identifier"])
	}
}

func TestUnsupportedAction(t *testing.T) {
	s := New(nil)
	code, out := call(t, s, "DeleteEverything", `{}`)
	if code != 400 {
		t.Fatalf("status = %d, want 400", code)
	}
	if out["__type"] != "InvalidArgumentException" {
		t.Fatalf("__type = %v", out["__type"])
	}
}
