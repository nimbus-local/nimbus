package cloudwatchmetrics

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"
)

func newSvc() *Service { return New("us-east-1") }

// jsonReq sends a JSON (awsJson1.0) request via X-Amz-Target.
func jsonReq(t *testing.T, s *Service, action string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	req := httptest.NewRequest(http.MethodPost, "/", &buf)
	req.Header.Set("X-Amz-Target", "GraniteServiceVersion20100801."+action)
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	return w
}

// cborReq sends a smithy-rpc-v2-cbor request via URL path.
func cborReq(t *testing.T, s *Service, action string, body map[string]interface{}) *httptest.ResponseRecorder {
	t.Helper()
	encoded := cborEncodeMap(body)
	req := httptest.NewRequest(http.MethodPost,
		"/service/GraniteServiceVersion20100801/operation/"+action,
		bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/cbor")
	req.Header.Set("smithy-protocol", "rpc-v2-cbor")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	return w
}

// decodeCBOR decodes the CBOR response body from a recorder.
func decodeCBOR(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	m, err := cborDecode(w.Body.Bytes())
	if err != nil {
		t.Fatalf("cborDecode: %v", err)
	}
	return m
}

// --- Detect ---

func TestDetect_JSONTarget(t *testing.T) {
	s := newSvc()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Amz-Target", "GraniteServiceVersion20100801.PutMetricData")
	if !s.Detect(req) {
		t.Fatal("expected Detect=true for JSON target")
	}
}

func TestDetect_CBORPath(t *testing.T) {
	s := newSvc()
	req := httptest.NewRequest(http.MethodPost,
		"/service/GraniteServiceVersion20100801/operation/PutMetricData", nil)
	if !s.Detect(req) {
		t.Fatal("expected Detect=true for CBOR path")
	}
}

func TestDetect_Miss(t *testing.T) {
	s := newSvc()
	req := httptest.NewRequest(http.MethodGet, "/other", nil)
	if s.Detect(req) {
		t.Fatal("expected Detect=false")
	}
}

// --- PutMetricData (JSON) ---

func TestPutMetricData(t *testing.T) {
	s := newSvc()
	w := jsonReq(t, s, "PutMetricData", map[string]interface{}{
		"Namespace": "TestNS",
		"MetricData": []map[string]interface{}{
			{"MetricName": "CPUUtilization", "Value": 42.5, "Unit": "Percent"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d\n%s", w.Code, w.Body.String())
	}
}

func TestPutMetricData_MissingNamespace(t *testing.T) {
	s := newSvc()
	w := jsonReq(t, s, "PutMetricData", map[string]interface{}{
		"MetricData": []map[string]interface{}{
			{"MetricName": "CPU", "Value": 1.0},
		},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- ListMetrics (JSON) ---

func TestListMetrics_Empty(t *testing.T) {
	s := newSvc()
	w := jsonReq(t, s, "ListMetrics", map[string]interface{}{})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	metrics, _ := resp["Metrics"].([]interface{})
	if len(metrics) != 0 {
		t.Errorf("expected empty Metrics, got %d", len(metrics))
	}
}

func TestListMetrics_AfterPut(t *testing.T) {
	s := newSvc()
	jsonReq(t, s, "PutMetricData", map[string]interface{}{
		"Namespace":  "MyApp",
		"MetricData": []map[string]interface{}{{"MetricName": "Latency", "Value": 10.0}},
	})

	w := jsonReq(t, s, "ListMetrics", map[string]interface{}{"Namespace": "MyApp"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Latency") {
		t.Error("expected metric name in ListMetrics response")
	}
}

func TestListMetrics_FilterByMetricName(t *testing.T) {
	s := newSvc()
	jsonReq(t, s, "PutMetricData", map[string]interface{}{
		"Namespace": "NS",
		"MetricData": []map[string]interface{}{
			{"MetricName": "Alpha", "Value": 1.0},
			{"MetricName": "Beta", "Value": 2.0},
		},
	})

	w := jsonReq(t, s, "ListMetrics", map[string]interface{}{"MetricName": "Alpha"})
	if strings.Contains(w.Body.String(), "Beta") {
		t.Error("expected Beta to be filtered out")
	}
	if !strings.Contains(w.Body.String(), "Alpha") {
		t.Error("expected Alpha in response")
	}
}

// --- GetMetricStatistics (JSON) ---

func TestGetMetricStatistics(t *testing.T) {
	s := newSvc()
	now := time.Now().UTC()
	jsonReq(t, s, "PutMetricData", map[string]interface{}{
		"Namespace": "TestNS",
		"MetricData": []map[string]interface{}{
			{
				"MetricName": "CPU",
				"Value":      75.0,
				"Unit":       "Percent",
				"Timestamp":  now.Format(time.RFC3339),
			},
		},
	})

	w := jsonReq(t, s, "GetMetricStatistics", map[string]interface{}{
		"Namespace":  "TestNS",
		"MetricName": "CPU",
		"StartTime":  now.Add(-time.Minute).Format(time.RFC3339),
		"EndTime":    now.Add(time.Minute).Format(time.RFC3339),
		"Period":     60,
		"Statistics": []string{"Average", "Sum", "Maximum"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	points, _ := resp["Datapoints"].([]interface{})
	if len(points) == 0 {
		t.Error("expected at least one datapoint")
	}
}

func TestGetMetricStatistics_NoData(t *testing.T) {
	s := newSvc()
	w := jsonReq(t, s, "GetMetricStatistics", map[string]interface{}{
		"Namespace":  "Empty",
		"MetricName": "NoSuchMetric",
		"Period":     60,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	points, _ := resp["Datapoints"].([]interface{})
	if len(points) != 0 {
		t.Errorf("expected empty Datapoints, got %d", len(points))
	}
}

// --- GetMetricData (JSON) ---

func TestGetMetricData(t *testing.T) {
	s := newSvc()
	now := time.Now().UTC()
	jsonReq(t, s, "PutMetricData", map[string]interface{}{
		"Namespace": "NS",
		"MetricData": []map[string]interface{}{
			{"MetricName": "M1", "Value": 5.0, "Timestamp": now.Format(time.RFC3339)},
		},
	})

	w := jsonReq(t, s, "GetMetricData", map[string]interface{}{
		"StartTime": now.Add(-time.Minute).Format(time.RFC3339),
		"EndTime":   now.Add(time.Minute).Format(time.RFC3339),
		"MetricDataQueries": []map[string]interface{}{
			{
				"Id": "q1",
				"MetricStat": map[string]interface{}{
					"Metric": map[string]interface{}{
						"Namespace":  "NS",
						"MetricName": "M1",
						"Dimensions": []interface{}{},
					},
					"Period": 60,
					"Stat":   "Sum",
				},
			},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "MetricDataResults") {
		t.Error("expected MetricDataResults in response")
	}
}

// --- Alarms (JSON) ---

func TestPutAndDescribeAlarm(t *testing.T) {
	s := newSvc()
	w := jsonReq(t, s, "PutMetricAlarm", map[string]interface{}{
		"AlarmName":          "high-cpu",
		"Namespace":          "AWS/EC2",
		"MetricName":         "CPUUtilization",
		"ComparisonOperator": "GreaterThanThreshold",
		"Threshold":          80.0,
		"EvaluationPeriods":  2,
		"Period":             300,
		"Statistic":          "Average",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("PutMetricAlarm: expected 200, got %d", w.Code)
	}

	w2 := jsonReq(t, s, "DescribeAlarms", map[string]interface{}{
		"AlarmNames": []string{"high-cpu"},
	})
	if w2.Code != http.StatusOK {
		t.Fatalf("DescribeAlarms: expected 200, got %d", w2.Code)
	}
	if !strings.Contains(w2.Body.String(), "high-cpu") {
		t.Error("expected alarm name in DescribeAlarms response")
	}
	if !strings.Contains(w2.Body.String(), `"StateValue":"OK"`) &&
		!strings.Contains(w2.Body.String(), `"StateValue": "OK"`) {
		t.Error("expected StateValue=OK")
	}
}

func TestPutMetricAlarm_MissingName(t *testing.T) {
	s := newSvc()
	w := jsonReq(t, s, "PutMetricAlarm", map[string]interface{}{
		"Namespace": "NS",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestDescribeAlarmsForMetric(t *testing.T) {
	s := newSvc()
	jsonReq(t, s, "PutMetricAlarm", map[string]interface{}{
		"AlarmName":          "cpu-alarm",
		"Namespace":          "AWS/EC2",
		"MetricName":         "CPUUtilization",
		"ComparisonOperator": "GreaterThanThreshold",
		"Threshold":          90.0,
		"EvaluationPeriods":  1,
		"Period":             60,
	})

	w := jsonReq(t, s, "DescribeAlarmsForMetric", map[string]interface{}{
		"Namespace":  "AWS/EC2",
		"MetricName": "CPUUtilization",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "cpu-alarm") {
		t.Error("expected alarm in DescribeAlarmsForMetric response")
	}
}

func TestDeleteAlarms(t *testing.T) {
	s := newSvc()
	jsonReq(t, s, "PutMetricAlarm", map[string]interface{}{
		"AlarmName": "to-delete", "Namespace": "NS", "MetricName": "M",
		"ComparisonOperator": "GreaterThanThreshold", "Threshold": 1.0,
		"EvaluationPeriods": 1, "Period": 60,
	})

	jsonReq(t, s, "DeleteAlarms", map[string]interface{}{"AlarmNames": []string{"to-delete"}})

	w := jsonReq(t, s, "DescribeAlarms", map[string]interface{}{"AlarmNames": []string{"to-delete"}})
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	alarms, _ := resp["MetricAlarms"].([]interface{})
	if len(alarms) != 0 {
		t.Errorf("expected empty MetricAlarms after delete, got %d", len(alarms))
	}
}

// --- Stub actions (JSON) ---

func TestSetAlarmState(t *testing.T) {
	s := newSvc()
	w := jsonReq(t, s, "SetAlarmState", map[string]interface{}{"AlarmName": "x"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestEnableAlarmActions(t *testing.T) {
	s := newSvc()
	w := jsonReq(t, s, "EnableAlarmActions", map[string]interface{}{})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestDisableAlarmActions(t *testing.T) {
	s := newSvc()
	w := jsonReq(t, s, "DisableAlarmActions", map[string]interface{}{})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// --- Tags (JSON) ---

func TestTagsRoundTrip(t *testing.T) {
	s := newSvc()
	arn := "arn:aws:cloudwatch:us-east-1:000000000000:alarm:my-alarm"

	jsonReq(t, s, "TagResource", map[string]interface{}{
		"ResourceARN": arn,
		"Tags":        []map[string]string{{"Key": "env", "Value": "dev"}},
	})

	w := jsonReq(t, s, "ListTagsForResource", map[string]interface{}{"ResourceARN": arn})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "env") {
		t.Error("expected tag key in ListTagsForResource response")
	}
}

func TestUntagResource(t *testing.T) {
	s := newSvc()
	arn := "arn:aws:cloudwatch:us-east-1:000000000000:alarm:x"
	jsonReq(t, s, "TagResource", map[string]interface{}{
		"ResourceARN": arn,
		"Tags":        []map[string]string{{"Key": "tmp", "Value": "yes"}},
	})
	jsonReq(t, s, "UntagResource", map[string]interface{}{
		"ResourceARN": arn,
		"TagKeys":     []string{"tmp"},
	})
	w := jsonReq(t, s, "ListTagsForResource", map[string]interface{}{"ResourceARN": arn})
	if strings.Contains(w.Body.String(), "tmp") {
		t.Error("expected removed tag to be absent")
	}
}

// --- Unknown action ---

func TestUnknownAction(t *testing.T) {
	s := newSvc()
	w := jsonReq(t, s, "NonExistentAction", map[string]interface{}{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- MetricsHandler (inspection) ---

func TestMetricsHandler(t *testing.T) {
	s := newSvc()
	jsonReq(t, s, "PutMetricData", map[string]interface{}{
		"Namespace":  "NS",
		"MetricData": []map[string]interface{}{{"MetricName": "M", "Value": 1.0}},
	})
	jsonReq(t, s, "PutMetricAlarm", map[string]interface{}{
		"AlarmName": "a", "Namespace": "NS", "MetricName": "M",
		"ComparisonOperator": "GreaterThanThreshold", "Threshold": 1.0,
		"EvaluationPeriods": 1, "Period": 60,
	})

	req := httptest.NewRequest(http.MethodGet, "/_nimbus/metrics", nil)
	w := httptest.NewRecorder()
	s.MetricsHandler(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "metrics") {
		t.Error("expected 'metrics' key in inspection response")
	}
}

// --- CBOR path ---

func TestCBOR_PutMetricData(t *testing.T) {
	s := newSvc()
	w := cborReq(t, s, "PutMetricData", map[string]interface{}{
		"Namespace": "CBOR_NS",
		"MetricData": []interface{}{
			map[string]interface{}{"MetricName": "Requests", "Value": float64(100)},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CBOR PutMetricData: expected 200, got %d", w.Code)
	}
	if w.Header().Get("smithy-protocol") != "rpc-v2-cbor" {
		t.Error("expected smithy-protocol: rpc-v2-cbor response header")
	}
	if w.Header().Get("Content-Type") != "application/cbor" {
		t.Error("expected Content-Type: application/cbor")
	}
}

func TestCBOR_PutMetricData_MissingNamespace(t *testing.T) {
	s := newSvc()
	w := cborReq(t, s, "PutMetricData", map[string]interface{}{
		"MetricData": []interface{}{},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestCBOR_ListMetrics(t *testing.T) {
	s := newSvc()
	cborReq(t, s, "PutMetricData", map[string]interface{}{
		"Namespace":  "CBOR_NS",
		"MetricData": []interface{}{map[string]interface{}{"MetricName": "CborMetric", "Value": float64(1)}},
	})

	w := cborReq(t, s, "ListMetrics", map[string]interface{}{"Namespace": "CBOR_NS"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	m := decodeCBOR(t, w)
	metrics, _ := m["Metrics"].([]interface{})
	if len(metrics) == 0 {
		t.Error("expected at least one metric in CBOR ListMetrics response")
	}
}

func TestCBOR_PutMetricAlarm(t *testing.T) {
	s := newSvc()
	w := cborReq(t, s, "PutMetricAlarm", map[string]interface{}{
		"AlarmName":          "cbor-alarm",
		"Namespace":          "NS",
		"MetricName":         "M",
		"ComparisonOperator": "GreaterThanThreshold",
		"Threshold":          float64(50),
		"EvaluationPeriods":  1,
		"Period":             60,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CBOR PutMetricAlarm: expected 200, got %d", w.Code)
	}
}

func TestCBOR_PutMetricAlarm_MissingName(t *testing.T) {
	s := newSvc()
	w := cborReq(t, s, "PutMetricAlarm", map[string]interface{}{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestCBOR_DescribeAlarms(t *testing.T) {
	s := newSvc()
	cborReq(t, s, "PutMetricAlarm", map[string]interface{}{
		"AlarmName": "cbor-a2", "Namespace": "NS", "MetricName": "M",
		"ComparisonOperator": "GreaterThanThreshold", "Threshold": float64(1),
		"EvaluationPeriods": 1, "Period": 60,
	})

	w := cborReq(t, s, "DescribeAlarms", map[string]interface{}{
		"AlarmNames": []interface{}{"cbor-a2"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CBOR DescribeAlarms: expected 200, got %d", w.Code)
	}
	// Response contains CBOR tag-1 epoch timestamps which the inline decoder
	// doesn't handle on the read side; verify protocol headers instead.
	if w.Header().Get("smithy-protocol") != "rpc-v2-cbor" {
		t.Error("expected smithy-protocol: rpc-v2-cbor header")
	}
	if len(w.Body.Bytes()) == 0 {
		t.Error("expected non-empty CBOR body")
	}
}

func TestCBOR_DeleteAlarms(t *testing.T) {
	s := newSvc()
	cborReq(t, s, "PutMetricAlarm", map[string]interface{}{
		"AlarmName": "del-cbor", "Namespace": "NS", "MetricName": "M",
		"ComparisonOperator": "GreaterThanThreshold", "Threshold": float64(1),
		"EvaluationPeriods": 1, "Period": 60,
	})
	w := cborReq(t, s, "DeleteAlarms", map[string]interface{}{
		"AlarmNames": []interface{}{"del-cbor"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestCBOR_StubActions(t *testing.T) {
	s := newSvc()
	for _, action := range []string{"SetAlarmState", "EnableAlarmActions", "DisableAlarmActions"} {
		w := cborReq(t, s, action, map[string]interface{}{})
		if w.Code != http.StatusOK {
			t.Errorf("CBOR %s: expected 200, got %d", action, w.Code)
		}
	}
}

func TestCBOR_TagsRoundTrip(t *testing.T) {
	s := newSvc()
	arn := "arn:aws:cloudwatch:us-east-1:000000000000:alarm:cbor-alarm"

	cborReq(t, s, "TagResource", map[string]interface{}{
		"ResourceARN": arn,
		"Tags":        []interface{}{map[string]interface{}{"Key": "team", "Value": "platform"}},
	})
	w := cborReq(t, s, "ListTagsForResource", map[string]interface{}{"ResourceARN": arn})
	if w.Code != http.StatusOK {
		t.Fatalf("CBOR ListTagsForResource: expected 200, got %d", w.Code)
	}
	m := decodeCBOR(t, w)
	tags, _ := m["Tags"].([]interface{})
	if len(tags) == 0 {
		t.Error("expected at least one tag")
	}
}

func TestCBOR_UntagResource(t *testing.T) {
	s := newSvc()
	arn := "arn:aws:cloudwatch:us-east-1:000000000000:alarm:cbor-alarm"
	cborReq(t, s, "TagResource", map[string]interface{}{
		"ResourceARN": arn,
		"Tags":        []interface{}{map[string]interface{}{"Key": "tmp", "Value": "yes"}},
	})
	cborReq(t, s, "UntagResource", map[string]interface{}{
		"ResourceARN": arn,
		"TagKeys":     []interface{}{"tmp"},
	})
	w := cborReq(t, s, "ListTagsForResource", map[string]interface{}{"ResourceARN": arn})
	m := decodeCBOR(t, w)
	tags, _ := m["Tags"].([]interface{})
	if len(tags) != 0 {
		t.Error("expected empty tags after untag")
	}
}

func TestCBOR_UnknownAction(t *testing.T) {
	s := newSvc()
	w := cborReq(t, s, "UnknownAction", map[string]interface{}{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestCBOR_DescribeAlarmsForMetric(t *testing.T) {
	s := newSvc()
	cborReq(t, s, "PutMetricAlarm", map[string]interface{}{
		"AlarmName": "cbor-for-metric", "Namespace": "NS2", "MetricName": "M2",
		"ComparisonOperator": "GreaterThanThreshold", "Threshold": float64(1),
		"EvaluationPeriods": 1, "Period": 60,
	})

	w := cborReq(t, s, "DescribeAlarmsForMetric", map[string]interface{}{
		"Namespace":  "NS2",
		"MetricName": "M2",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	// Response contains CBOR tag-1 epoch timestamps; check protocol headers.
	if w.Header().Get("smithy-protocol") != "rpc-v2-cbor" {
		t.Error("expected smithy-protocol: rpc-v2-cbor header")
	}
	if len(w.Body.Bytes()) == 0 {
		t.Error("expected non-empty CBOR body")
	}
}

// --- CBOR Timestamp shapes (tag 1) ---

func TestCBOR_DecodeTag1Timestamp(t *testing.T) {
	// Uint epoch (what CborEpochTime encodes).
	enc := cborEncodeMap(map[string]interface{}{"Timestamp": CborEpochTime(1751980800)})
	m, err := cborDecode(enc)
	if err != nil {
		t.Fatalf("cborDecode: %v", err)
	}
	ts, ok := m["Timestamp"].(time.Time)
	if !ok || ts.Unix() != 1751980800 {
		t.Fatalf("Timestamp = %#v, want time.Time at 1751980800", m["Timestamp"])
	}

	// Float epoch with fractional seconds (how the AWS SDK encodes tag 1).
	raw := append([]byte{0xa1}, cborText("T")...) // 1-entry map, key "T"
	raw = append(raw, 0xc1, 0xfb)
	bits := math.Float64bits(1751980800.5)
	for i := 7; i >= 0; i-- {
		raw = append(raw, byte(bits>>(uint(i)*8)))
	}
	m, err = cborDecode(raw)
	if err != nil {
		t.Fatalf("cborDecode float epoch: %v", err)
	}
	ts, ok = m["T"].(time.Time)
	if !ok || ts.Unix() != 1751980800 || ts.Nanosecond() != 5e8 {
		t.Fatalf("float epoch = %#v, want 1751980800.5", m["T"])
	}
}

func TestCBOR_PutGetMetricData_TimestampRoundTrip(t *testing.T) {
	s := newSvc()
	epoch := time.Now().Add(-2 * time.Minute).Truncate(time.Minute).Unix()

	w := cborReq(t, s, "PutMetricData", map[string]interface{}{
		"Namespace": "CBOR_NS",
		"MetricData": []interface{}{map[string]interface{}{
			"MetricName": "Requests",
			"Timestamp":  CborEpochTime(epoch),
			"Value":      float64(180),
		}},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("PutMetricData with Timestamp: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	w = cborReq(t, s, "GetMetricData", map[string]interface{}{
		"StartTime": CborEpochTime(epoch - 600),
		"EndTime":   CborEpochTime(epoch + 600),
		"MetricDataQueries": []interface{}{map[string]interface{}{
			"Id": "m0",
			"MetricStat": map[string]interface{}{
				"Metric": map[string]interface{}{
					"Namespace":  "CBOR_NS",
					"MetricName": "Requests",
				},
				"Period": 60,
				"Stat":   "Sum",
			},
		}},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("GetMetricData with tag-1 window: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	resp := decodeCBOR(t, w)
	results, _ := resp["MetricDataResults"].([]interface{})
	if len(results) != 1 {
		t.Fatalf("results = %#v", resp)
	}
	r := results[0].(map[string]interface{})
	tsList, _ := r["Timestamps"].([]interface{})
	vals, _ := r["Values"].([]interface{})
	if len(tsList) != 1 || len(vals) != 1 {
		t.Fatalf("datapoints = %#v / %#v, want the seeded point back", tsList, vals)
	}
	// Response timestamps must be tag-1 (decode back to time.Time), not strings.
	ts, ok := tsList[0].(time.Time)
	if !ok || ts.Unix() != epoch {
		t.Errorf("response Timestamp = %#v, want tag-1 time at %d", tsList[0], epoch)
	}
	if v, _ := vals[0].(float64); v != 180 {
		t.Errorf("Value = %v, want 180", vals[0])
	}
}

// --- ListMetrics dimension filters ---

// seedDimensionedMetrics publishes the shape argus discovers Service Connect
// edges from: one RequestCount series carrying TargetDiscoveryName and one
// without it, plus an unrelated metric.
func seedDimensionedMetrics(t *testing.T, s *Service) {
	t.Helper()
	jsonReq(t, s, "PutMetricData", map[string]interface{}{
		"Namespace": "AWS/ECS",
		"MetricData": []map[string]interface{}{{
			"MetricName": "RequestCount",
			"Dimensions": []map[string]string{
				{"Name": "ClusterName", "Value": "demo-cluster"},
				{"Name": "ServiceName", "Value": "web-svc"},
				{"Name": "TargetDiscoveryName", "Value": "api"},
			},
			"Value": 120.0,
		}},
	})
	jsonReq(t, s, "PutMetricData", map[string]interface{}{
		"Namespace": "AWS/ECS",
		"MetricData": []map[string]interface{}{{
			"MetricName": "RequestCount",
			"Dimensions": []map[string]string{
				{"Name": "ClusterName", "Value": "demo-cluster"},
				{"Name": "ServiceName", "Value": "web-svc"},
			},
			"Value": 55.0,
		}},
	})
	jsonReq(t, s, "PutMetricData", map[string]interface{}{
		"Namespace": "AWS/ECS",
		"MetricData": []map[string]interface{}{{
			"MetricName": "CpuUtilized",
			"Dimensions": []map[string]string{
				{"Name": "TargetDiscoveryName", "Value": "api"},
			},
			"Value": 3.0,
		}},
	})
}

// listMetricNames returns the (MetricName, dimension-count) pairs a ListMetrics
// response contains, so a test can assert on which series matched.
func listedSeries(t *testing.T, w *httptest.ResponseRecorder) []string {
	t.Helper()
	var resp struct {
		Metrics []struct {
			Namespace  string              `json:"Namespace"`
			MetricName string              `json:"MetricName"`
			Dimensions []map[string]string `json:"Dimensions"`
		} `json:"Metrics"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var out []string
	for _, m := range resp.Metrics {
		dims := map[string]string{}
		for _, d := range m.Dimensions {
			dims[d["Name"]] = d["Value"]
		}
		out = append(out, fmt.Sprintf("%s/%s TargetDiscoveryName=%q",
			m.Namespace, m.MetricName, dims["TargetDiscoveryName"]))
	}
	sort.Strings(out)
	return out
}

// A DimensionFilter with only a Name matches every metric carrying a dimension
// of that name, whatever its value. This is the argus Phase 4 discovery query.
func TestListMetrics_DimensionFilterNameOnly(t *testing.T) {
	s := newSvc()
	seedDimensionedMetrics(t, s)

	w := jsonReq(t, s, "ListMetrics", map[string]interface{}{
		"Namespace":  "AWS/ECS",
		"MetricName": "RequestCount",
		"Dimensions": []map[string]string{{"Name": "TargetDiscoveryName"}},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	got := listedSeries(t, w)
	want := []string{`AWS/ECS/RequestCount TargetDiscoveryName="api"`}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("name-only filter: got %v, want %v", got, want)
	}
}

// The bug this replaces: a name-only filter was read as "this dimension equals
// the empty string", which selected the metrics *lacking* the dimension.
func TestListMetrics_DimensionFilterNameOnlyIsNotInverted(t *testing.T) {
	s := newSvc()
	seedDimensionedMetrics(t, s)

	w := jsonReq(t, s, "ListMetrics", map[string]interface{}{
		"Namespace":  "AWS/ECS",
		"MetricName": "RequestCount",
		"Dimensions": []map[string]string{{"Name": "TargetDiscoveryName"}},
	})
	for _, got := range listedSeries(t, w) {
		if strings.Contains(got, `TargetDiscoveryName=""`) {
			t.Errorf("series without the dimension matched a name-only filter: %s", got)
		}
	}
}

func TestListMetrics_DimensionFilterNameAndValue(t *testing.T) {
	s := newSvc()
	seedDimensionedMetrics(t, s)

	w := jsonReq(t, s, "ListMetrics", map[string]interface{}{
		"Namespace":  "AWS/ECS",
		"MetricName": "RequestCount",
		"Dimensions": []map[string]string{{"Name": "TargetDiscoveryName", "Value": "api"}},
	})
	if got := listedSeries(t, w); len(got) != 1 {
		t.Errorf("exact-pair filter: got %v, want 1 series", got)
	}

	w = jsonReq(t, s, "ListMetrics", map[string]interface{}{
		"Namespace":  "AWS/ECS",
		"MetricName": "RequestCount",
		"Dimensions": []map[string]string{{"Name": "TargetDiscoveryName", "Value": "nope"}},
	})
	if got := listedSeries(t, w); len(got) != 0 {
		t.Errorf("filter on an unpublished value should match nothing, got %v", got)
	}
}

// Multiple filters are ANDed: a metric must satisfy every one.
func TestListMetrics_DimensionFiltersAreANDed(t *testing.T) {
	s := newSvc()
	seedDimensionedMetrics(t, s)

	w := jsonReq(t, s, "ListMetrics", map[string]interface{}{
		"Namespace": "AWS/ECS",
		"Dimensions": []map[string]string{
			{"Name": "ServiceName", "Value": "web-svc"},
			{"Name": "TargetDiscoveryName"},
		},
	})
	// Only the RequestCount series has both; CpuUtilized carries
	// TargetDiscoveryName but no ServiceName.
	got := listedSeries(t, w)
	if len(got) != 1 || !strings.Contains(got[0], "RequestCount") {
		t.Errorf("ANDed filters: got %v, want just the RequestCount series", got)
	}
}

// A filter naming a dimension no metric has must match nothing — not everything.
func TestListMetrics_DimensionFilterUnknownName(t *testing.T) {
	s := newSvc()
	seedDimensionedMetrics(t, s)

	w := jsonReq(t, s, "ListMetrics", map[string]interface{}{
		"Namespace":  "AWS/ECS",
		"Dimensions": []map[string]string{{"Name": "NoSuchDimension"}},
	})
	if got := listedSeries(t, w); len(got) != 0 {
		t.Errorf("expected no matches for an unknown dimension name, got %v", got)
	}
}

// Dimension filters must not narrow a request that has none.
func TestListMetrics_NoDimensionFilterReturnsAll(t *testing.T) {
	s := newSvc()
	seedDimensionedMetrics(t, s)

	w := jsonReq(t, s, "ListMetrics", map[string]interface{}{"Namespace": "AWS/ECS"})
	if got := listedSeries(t, w); len(got) != 3 {
		t.Errorf("expected all 3 seeded series, got %v", got)
	}
}

// The Terraform provider reaches CloudWatch over smithy-rpc-v2-cbor, which
// parses filters through a separate path — it must agree with the JSON one.
func TestCBOR_ListMetrics_DimensionFilterNameOnly(t *testing.T) {
	s := newSvc()
	seedDimensionedMetrics(t, s)

	w := cborReq(t, s, "ListMetrics", map[string]interface{}{
		"Namespace":  "AWS/ECS",
		"MetricName": "RequestCount",
		"Dimensions": []interface{}{
			map[string]interface{}{"Name": "TargetDiscoveryName"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	metrics, _ := decodeCBOR(t, w)["Metrics"].([]interface{})
	if len(metrics) != 1 {
		t.Fatalf("name-only CBOR filter: expected 1 series, got %d", len(metrics))
	}
	dims, _ := metrics[0].(map[string]interface{})["Dimensions"].([]interface{})
	found := false
	for _, d := range dims {
		if dm, ok := d.(map[string]interface{}); ok && dm["Name"] == "TargetDiscoveryName" {
			found = true
		}
	}
	if !found {
		t.Error("matched series does not carry the filtered dimension")
	}
}

// matchDims keeps its exact-match semantics: GetMetricStatistics and the alarm
// lookups identify one series and must not adopt DimensionFilter behaviour.
func TestMatchDimsKeepsExactSemantics(t *testing.T) {
	target := map[string]string{"ClusterName": "demo", "ServiceName": "web"}
	if !matchDims(target, map[string]string{"ClusterName": "demo"}) {
		t.Error("matchDims should match a subset with equal values")
	}
	if matchDims(target, map[string]string{"ClusterName": "other"}) {
		t.Error("matchDims should reject a differing value")
	}
	// Still the old behaviour here, and deliberately so: an empty value means
	// "absent" to the exact matcher.
	if matchDims(target, map[string]string{"Missing": ""}) != true {
		t.Error("matchDims treats an empty filter value as absent")
	}
}
