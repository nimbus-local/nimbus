// Package cloudwatchmetrics emulates the AWS CloudWatch Metrics API.
// All metric time-series and alarms are stored in-memory. PutMetricData
// accepts any namespace/metric/dimension combination and keeps the most
// recent 10,000 points per series. Alarms are structural only — state is
// always OK and no evaluation logic runs.
package cloudwatchmetrics

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nimbus-local/nimbus/internal/uid"
)

const (
	accountID  = "000000000000"
	cwTarget   = "GraniteServiceVersion20100801."
	cwCBORPath = "/service/GraniteServiceVersion20100801/operation/"
	maxPoints  = 10_000
)

// Service implements the AWS CloudWatch Metrics emulator.
type Service struct {
	mu      sync.RWMutex
	metrics map[string][]*metricSeries // "namespace/metricName" -> list of series
	alarms  map[string]*alarm
	tags    map[string]map[string]string // arn -> tags
	region  string
}

type dataPoint struct {
	timestamp time.Time
	value     float64
	unit      string
}

type metricSeries struct {
	namespace  string
	metricName string
	dimensions map[string]string
	points     []dataPoint
}

type alarm struct {
	name               string
	arn                string
	namespace          string
	metricName         string
	comparisonOperator string
	threshold          float64
	evaluationPeriods  int
	period             int
	statistic          string
	unit               string
	description        string
	createdAt          time.Time
	dimensions         map[string]string
}

func New(region string) *Service {
	if region == "" {
		region = "us-east-1"
	}
	return &Service{
		region:  region,
		metrics: map[string][]*metricSeries{},
		alarms:  map[string]*alarm{},
		tags:    map[string]map[string]string{},
	}
}

func (s *Service) Name() string { return "cloudwatchmetrics" }

// Reset clears all in-memory state.
func (s *Service) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metrics = map[string][]*metricSeries{}
	s.alarms = map[string]*alarm{}
	s.tags = map[string]map[string]string{}
}

func (s *Service) Detect(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), cwTarget) ||
		strings.HasPrefix(r.URL.Path, cwCBORPath)
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// TF provider v6 uses smithy-rpc-v2-cbor: action is in the URL path.
	if strings.HasPrefix(r.URL.Path, cwCBORPath) {
		s.serveCBOR(w, r)
		return
	}
	// AWS CLI uses awsJson1.0: action is in the X-Amz-Target header.
	action := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), cwTarget)
	switch action {
	case "PutMetricData":
		s.putMetricData(w, r)
	case "ListMetrics":
		s.listMetrics(w, r)
	case "GetMetricStatistics":
		s.getMetricStatistics(w, r)
	case "GetMetricData":
		s.getMetricData(w, r)
	case "PutMetricAlarm":
		s.putMetricAlarm(w, r)
	case "DescribeAlarms":
		s.describeAlarms(w, r)
	case "DescribeAlarmsForMetric":
		s.describeAlarmsForMetric(w, r)
	case "DeleteAlarms":
		s.deleteAlarms(w, r)
	case "SetAlarmState", "EnableAlarmActions", "DisableAlarmActions":
		writeJSON(w, http.StatusOK, map[string]interface{}{})
	case "ListTagsForResource":
		s.listTagsForResource(w, r)
	case "TagResource":
		s.tagResource(w, r)
	case "UntagResource":
		s.untagResource(w, r)
	default:
		cwError(w, http.StatusBadRequest, "InvalidAction",
			fmt.Sprintf("Action %s is not supported.", action))
	}
}

// ── PutMetricData ─────────────────────────────────────────────────────────────

func (s *Service) putMetricData(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Namespace  string `json:"Namespace"`
		MetricData []struct {
			MetricName string `json:"MetricName"`
			Dimensions []struct {
				Name  string `json:"Name"`
				Value string `json:"Value"`
			} `json:"Dimensions"`
			Timestamp string  `json:"Timestamp"`
			Value     float64 `json:"Value"`
			Unit      string  `json:"Unit"`
		} `json:"MetricData"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Namespace == "" {
		cwError(w, http.StatusBadRequest, "MissingParameter", "Namespace is required")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, md := range req.MetricData {
		dims := make(map[string]string)
		for _, d := range md.Dimensions {
			dims[d.Name] = d.Value
		}
		var ts time.Time
		if md.Timestamp != "" {
			ts, _ = time.Parse(time.RFC3339, md.Timestamp)
		}
		if ts.IsZero() {
			ts = time.Now().UTC()
		}
		unit := md.Unit
		if unit == "" {
			unit = "None"
		}
		series := s.findOrCreate(req.Namespace, md.MetricName, dims)
		series.points = append(series.points, dataPoint{timestamp: ts, value: md.Value, unit: unit})
		if len(series.points) > maxPoints {
			series.points = series.points[len(series.points)-maxPoints:]
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{})
}

// ── ListMetrics ───────────────────────────────────────────────────────────────

func (s *Service) listMetrics(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Namespace  string `json:"Namespace"`
		MetricName string `json:"MetricName"`
		Dimensions []struct {
			Name  string `json:"Name"`
			Value string `json:"Value"`
		} `json:"Dimensions"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	// A DimensionFilter must name a dimension; one without a Name constrains
	// nothing, so it is dropped rather than matched against the empty name.
	dimFilter := make(map[string]string)
	for _, d := range req.Dimensions {
		if d.Name == "" {
			continue
		}
		dimFilter[d.Name] = d.Value
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	type metricJSON struct {
		Namespace  string              `json:"Namespace"`
		MetricName string              `json:"MetricName"`
		Dimensions []map[string]string `json:"Dimensions"`
	}

	var metrics []metricJSON
	for _, seriesList := range s.metrics {
		for _, series := range seriesList {
			if req.Namespace != "" && series.namespace != req.Namespace {
				continue
			}
			if req.MetricName != "" && series.metricName != req.MetricName {
				continue
			}
			if !matchDimFilters(series.dimensions, dimFilter) {
				continue
			}
			var dims []map[string]string
			for k, v := range series.dimensions {
				dims = append(dims, map[string]string{"Name": k, "Value": v})
			}
			if dims == nil {
				dims = []map[string]string{}
			}
			metrics = append(metrics, metricJSON{
				Namespace:  series.namespace,
				MetricName: series.metricName,
				Dimensions: dims,
			})
		}
	}
	if metrics == nil {
		metrics = []metricJSON{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"Metrics":   metrics,
		"NextToken": "",
	})
}

// ── GetMetricStatistics ───────────────────────────────────────────────────────

func (s *Service) getMetricStatistics(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Namespace  string `json:"Namespace"`
		MetricName string `json:"MetricName"`
		Dimensions []struct {
			Name  string `json:"Name"`
			Value string `json:"Value"`
		} `json:"Dimensions"`
		StartTime  string   `json:"StartTime"`
		EndTime    string   `json:"EndTime"`
		Period     int      `json:"Period"`
		Statistics []string `json:"Statistics"`
		Unit       string   `json:"Unit"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	dims := make(map[string]string)
	for _, d := range req.Dimensions {
		dims[d.Name] = d.Value
	}
	var start, end time.Time
	start, _ = time.Parse(time.RFC3339, req.StartTime)
	end, _ = time.Parse(time.RFC3339, req.EndTime)
	period := req.Period
	if period <= 0 {
		period = 60
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	type datapointJSON struct {
		Timestamp   string  `json:"Timestamp"`
		SampleCount float64 `json:"SampleCount"`
		Average     float64 `json:"Average,omitempty"`
		Sum         float64 `json:"Sum,omitempty"`
		Minimum     float64 `json:"Minimum,omitempty"`
		Maximum     float64 `json:"Maximum,omitempty"`
		Unit        string  `json:"Unit"`
	}

	var datapoints []datapointJSON
	if series := s.find(req.Namespace, req.MetricName, dims); series != nil {
		for _, b := range aggregateToBuckets(series.points, start, end, period, req.Unit) {
			dp := datapointJSON{
				Timestamp:   b.start.UTC().Format(time.RFC3339),
				SampleCount: b.count,
				Unit:        b.unit,
			}
			for _, stat := range req.Statistics {
				switch stat {
				case "Average":
					dp.Average = b.average()
				case "Sum":
					dp.Sum = b.sum
				case "Minimum":
					dp.Minimum = b.min
				case "Maximum":
					dp.Maximum = b.max
				}
			}
			datapoints = append(datapoints, dp)
		}
	}
	if datapoints == nil {
		datapoints = []datapointJSON{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"Label":      req.MetricName,
		"Datapoints": datapoints,
	})
}

// ── GetMetricData ─────────────────────────────────────────────────────────────

func (s *Service) getMetricData(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StartTime         string `json:"StartTime"`
		EndTime           string `json:"EndTime"`
		MetricDataQueries []struct {
			Id         string `json:"Id"`
			MetricStat struct {
				Metric struct {
					Namespace  string `json:"Namespace"`
					MetricName string `json:"MetricName"`
					Dimensions []struct {
						Name  string `json:"Name"`
						Value string `json:"Value"`
					} `json:"Dimensions"`
				} `json:"Metric"`
				Period int    `json:"Period"`
				Stat   string `json:"Stat"`
			} `json:"MetricStat"`
		} `json:"MetricDataQueries"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	var start, end time.Time
	start, _ = time.Parse(time.RFC3339, req.StartTime)
	end, _ = time.Parse(time.RFC3339, req.EndTime)

	s.mu.RLock()
	defer s.mu.RUnlock()

	type resultJSON struct {
		Id         string    `json:"Id"`
		Label      string    `json:"Label"`
		Timestamps []string  `json:"Timestamps"`
		Values     []float64 `json:"Values"`
		StatusCode string    `json:"StatusCode"`
	}

	var results []resultJSON
	for _, q := range req.MetricDataQueries {
		dims := make(map[string]string)
		for _, d := range q.MetricStat.Metric.Dimensions {
			dims[d.Name] = d.Value
		}
		period := q.MetricStat.Period
		if period <= 0 {
			period = 60
		}
		res := resultJSON{
			Id:         q.Id,
			Label:      q.MetricStat.Metric.MetricName,
			Timestamps: []string{},
			Values:     []float64{},
			StatusCode: "Complete",
		}
		if series := s.find(q.MetricStat.Metric.Namespace, q.MetricStat.Metric.MetricName, dims); series != nil {
			for _, b := range aggregateToBuckets(series.points, start, end, period, "") {
				res.Timestamps = append(res.Timestamps, b.start.UTC().Format(time.RFC3339))
				res.Values = append(res.Values, b.statValue(q.MetricStat.Stat))
			}
		}
		results = append(results, res)
	}
	if results == nil {
		results = []resultJSON{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"MetricDataResults": results,
		"Messages":          []interface{}{},
	})
}

// ── Alarms ────────────────────────────────────────────────────────────────────

func (s *Service) putMetricAlarm(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AlarmName          string  `json:"AlarmName"`
		AlarmDescription   string  `json:"AlarmDescription"`
		Namespace          string  `json:"Namespace"`
		MetricName         string  `json:"MetricName"`
		ComparisonOperator string  `json:"ComparisonOperator"`
		Threshold          float64 `json:"Threshold"`
		EvaluationPeriods  int     `json:"EvaluationPeriods"`
		Period             int     `json:"Period"`
		Statistic          string  `json:"Statistic"`
		Unit               string  `json:"Unit"`
		Dimensions         []struct {
			Name  string `json:"Name"`
			Value string `json:"Value"`
		} `json:"Dimensions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.AlarmName == "" {
		cwError(w, http.StatusBadRequest, "MissingParameter", "AlarmName is required")
		return
	}

	dims := make(map[string]string)
	for _, d := range req.Dimensions {
		dims[d.Name] = d.Value
	}

	s.mu.Lock()
	s.alarms[req.AlarmName] = &alarm{
		name:               req.AlarmName,
		arn:                s.alarmARN(req.AlarmName),
		namespace:          req.Namespace,
		metricName:         req.MetricName,
		comparisonOperator: req.ComparisonOperator,
		threshold:          req.Threshold,
		evaluationPeriods:  req.EvaluationPeriods,
		period:             req.Period,
		statistic:          req.Statistic,
		unit:               req.Unit,
		description:        req.AlarmDescription,
		createdAt:          time.Now().UTC(),
		dimensions:         dims,
	}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) describeAlarms(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AlarmNames []string `json:"AlarmNames"`
		StateValue string   `json:"StateValue"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.RLock()
	defer s.mu.RUnlock()

	var alarms []map[string]interface{}
	for _, a := range s.alarms {
		if len(req.AlarmNames) > 0 && !containsStr(req.AlarmNames, a.name) {
			continue
		}
		if req.StateValue != "" && req.StateValue != "OK" {
			continue
		}
		alarms = append(alarms, s.alarmMap(a))
	}
	if alarms == nil {
		alarms = []map[string]interface{}{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"MetricAlarms":    alarms,
		"CompositeAlarms": []interface{}{},
	})
}

func (s *Service) describeAlarmsForMetric(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Namespace  string `json:"Namespace"`
		MetricName string `json:"MetricName"`
		Dimensions []struct {
			Name  string `json:"Name"`
			Value string `json:"Value"`
		} `json:"Dimensions"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	dims := make(map[string]string)
	for _, d := range req.Dimensions {
		dims[d.Name] = d.Value
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var alarms []map[string]interface{}
	for _, a := range s.alarms {
		if a.namespace != req.Namespace || a.metricName != req.MetricName {
			continue
		}
		if !matchDims(a.dimensions, dims) {
			continue
		}
		alarms = append(alarms, s.alarmMap(a))
	}
	if alarms == nil {
		alarms = []map[string]interface{}{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"MetricAlarms": alarms})
}

func (s *Service) deleteAlarms(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AlarmNames []string `json:"AlarmNames"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.Lock()
	for _, name := range req.AlarmNames {
		delete(s.alarms, name)
	}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) alarmMap(a *alarm) map[string]interface{} {
	var dims []map[string]string
	for k, v := range a.dimensions {
		dims = append(dims, map[string]string{"Name": k, "Value": v})
	}
	if dims == nil {
		dims = []map[string]string{}
	}
	ts := a.createdAt.UTC().Format(time.RFC3339)
	return map[string]interface{}{
		"AlarmName":                          a.name,
		"AlarmArn":                           a.arn,
		"AlarmDescription":                   a.description,
		"AlarmConfigurationUpdatedTimestamp": ts,
		"ActionsEnabled":                     true,
		"OKActions":                          []string{},
		"AlarmActions":                       []string{},
		"InsufficientDataActions":            []string{},
		"StateValue":                         "OK",
		"StateReason":                        "Nimbus local emulator — state is always OK",
		"StateReasonData":                    "",
		"StateUpdatedTimestamp":              ts,
		"MetricName":                         a.metricName,
		"Namespace":                          a.namespace,
		"Statistic":                          a.statistic,
		"Dimensions":                         dims,
		"Period":                             a.period,
		"EvaluationPeriods":                  a.evaluationPeriods,
		"DatapointsToAlarm":                  a.evaluationPeriods,
		"Threshold":                          a.threshold,
		"ComparisonOperator":                 a.comparisonOperator,
		"TreatMissingData":                   "missing",
	}
}

func (s *Service) alarmARN(name string) string {
	return fmt.Sprintf("arn:aws:cloudwatch:%s:%s:alarm:%s", s.region, accountID, name)
}

// ── Tags ──────────────────────────────────────────────────────────────────────

func (s *Service) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceARN string `json:"ResourceARN"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.RLock()
	tags := s.tags[req.ResourceARN]
	s.mu.RUnlock()

	var tagList []map[string]string
	for k, v := range tags {
		tagList = append(tagList, map[string]string{"Key": k, "Value": v})
	}
	if tagList == nil {
		tagList = []map[string]string{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"Tags": tagList})
}

func (s *Service) tagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceARN string `json:"ResourceARN"`
		Tags        []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"Tags"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.Lock()
	if s.tags[req.ResourceARN] == nil {
		s.tags[req.ResourceARN] = map[string]string{}
	}
	for _, t := range req.Tags {
		s.tags[req.ResourceARN][t.Key] = t.Value
	}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) untagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceARN string   `json:"ResourceARN"`
		TagKeys     []string `json:"TagKeys"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.mu.Lock()
	if tags, ok := s.tags[req.ResourceARN]; ok {
		for _, k := range req.TagKeys {
			delete(tags, k)
		}
	}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]interface{}{})
}

// ── Inspection endpoint ───────────────────────────────────────────────────────

// MetricsHandler serves /_nimbus/metrics.
func (s *Service) MetricsHandler(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	type metricEntry struct {
		Namespace  string `json:"namespace"`
		MetricName string `json:"metricName"`
		Points     int    `json:"points"`
	}
	type alarmEntry struct {
		Name  string `json:"name"`
		ARN   string `json:"arn"`
		State string `json:"state"`
	}

	var metrics []metricEntry
	for _, seriesList := range s.metrics {
		for _, series := range seriesList {
			metrics = append(metrics, metricEntry{
				Namespace:  series.namespace,
				MetricName: series.metricName,
				Points:     len(series.points),
			})
		}
	}
	var alarms []alarmEntry
	for _, a := range s.alarms {
		alarms = append(alarms, alarmEntry{Name: a.name, ARN: a.arn, State: "OK"})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"metrics": metrics,
		"alarms":  alarms,
	})
}

// ── Storage helpers ───────────────────────────────────────────────────────────

func (s *Service) findOrCreate(namespace, metricName string, dims map[string]string) *metricSeries {
	key := namespace + "/" + metricName
	for _, series := range s.metrics[key] {
		if dimsEqual(series.dimensions, dims) {
			return series
		}
	}
	series := &metricSeries{namespace: namespace, metricName: metricName, dimensions: dims}
	s.metrics[key] = append(s.metrics[key], series)
	return series
}

// find returns the series stored under exactly the requested dimension set, or
// nil when no series has it.
//
// The dimension set identifies the metric in the data-query APIs
// (GetMetricData, GetMetricStatistics) — it is not a filter. A metric stored as
// [a=1, b=2] is a distinct metric from [a=1] and from the dimensionless one, so
// matching a subset here would let any two clients that share a metric name but
// use different dimension granularities read each other's data. Storage agrees:
// findOrCreate keys a series on its full dimension set.
func (s *Service) find(namespace, metricName string, dims map[string]string) *metricSeries {
	for _, series := range s.metrics[namespace+"/"+metricName] {
		if dimsEqual(series.dimensions, dims) {
			return series
		}
	}
	return nil
}

// dimsEqual reports whether two dimension sets are identical.
func dimsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// matchDims reports whether target carries every name/value pair in filter.
// This is *subset* matching: extra dimensions on target are ignored, so a
// filter of [a=1] matches a target of [a=1, b=2].
//
// Only the alarm lookup (DescribeAlarmsForMetric) uses it. The data-query APIs
// must not — see find, where a subset match would return another metric's data.
func matchDims(target, filter map[string]string) bool {
	for k, v := range filter {
		if target[k] != v {
			return false
		}
	}
	return true
}

// matchDimFilters applies ListMetrics DimensionFilter semantics, which differ
// from both the subset matching above and the exact identity find uses: a
// filter naming a dimension with no value matches every metric that *carries* a
// dimension of that name, whatever its value, and one naming both matches only
// that exact pair. Filters are ANDed.
//
// matchDims cannot express the name-only case. A missing map key and an empty
// value both read as "", so it treats a name-only filter as "this dimension
// equals the empty string" — which selects exactly the metrics lacking the
// dimension, the inverse of what the caller asked for.
func matchDimFilters(target, filters map[string]string) bool {
	for name, value := range filters {
		have, ok := target[name]
		if !ok {
			return false
		}
		if value != "" && have != value {
			return false
		}
	}
	return true
}

// ── Aggregation ───────────────────────────────────────────────────────────────

type bucket struct {
	start time.Time
	sum   float64
	min   float64
	max   float64
	count float64
	unit  string
}

func (b *bucket) average() float64 {
	if b.count == 0 {
		return 0
	}
	return b.sum / b.count
}

func (b *bucket) statValue(stat string) float64 {
	switch stat {
	case "Sum":
		return b.sum
	case "Average":
		return b.average()
	case "Minimum":
		return b.min
	case "Maximum":
		return b.max
	case "SampleCount":
		return b.count
	default:
		return b.average()
	}
}

func aggregateToBuckets(points []dataPoint, start, end time.Time, period int, unitFilter string) []bucket {
	if start.IsZero() {
		start = time.Now().Add(-time.Hour)
	}
	if end.IsZero() {
		end = time.Now()
	}
	dur := time.Duration(period) * time.Second
	bucketMap := map[int64]*bucket{}

	for _, p := range points {
		if p.timestamp.Before(start) || p.timestamp.After(end) {
			continue
		}
		if unitFilter != "" && unitFilter != "None" && p.unit != unitFilter {
			continue
		}
		slot := p.timestamp.Truncate(dur).Unix()
		b := bucketMap[slot]
		if b == nil {
			b = &bucket{start: p.timestamp.Truncate(dur), min: math.MaxFloat64, max: -math.MaxFloat64, unit: p.unit}
			bucketMap[slot] = b
		}
		b.sum += p.value
		b.count++
		if p.value < b.min {
			b.min = p.value
		}
		if p.value > b.max {
			b.max = p.value
		}
	}

	slots := make([]int64, 0, len(bucketMap))
	for slot := range bucketMap {
		slots = append(slots, slot)
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i] < slots[j] })

	result := make([]bucket, len(slots))
	for i, slot := range slots {
		result[i] = *bucketMap[slot]
	}
	return result
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func containsStr(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.0")
	w.Header().Set("x-amzn-requestid", uid.New())
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func cwError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"__type": code, "message": msg})
}

// ── smithy-rpc-v2-cbor handlers (TF provider v6) ─────────────────────────────
// The AWS SDK Go v2 uses smithy-rpc-v2-cbor for CloudWatch. Requests arrive
// at /service/GraniteServiceVersion20100801/operation/{Action} with CBOR
// bodies and smithy-protocol: rpc-v2-cbor request header. Responses must also
// be CBOR with that header set.

func (s *Service) serveCBOR(w http.ResponseWriter, r *http.Request) {
	action := strings.TrimPrefix(r.URL.Path, cwCBORPath)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.cborError(w, http.StatusBadRequest, "InvalidRequest", "cannot read body")
		return
	}

	var params map[string]interface{}
	if len(body) > 0 {
		params, err = cborDecode(body)
		if err != nil {
			s.cborError(w, http.StatusBadRequest, "InvalidRequest", "cannot decode CBOR body")
			return
		}
	} else {
		params = map[string]interface{}{}
	}

	switch action {
	case "PutMetricData":
		s.cborPutMetricData(w, params)
	case "ListMetrics":
		s.cborListMetrics(w, params)
	case "GetMetricStatistics":
		s.cborGetMetricStatistics(w, params)
	case "GetMetricData":
		s.cborGetMetricData(w, params)
	case "PutMetricAlarm":
		s.cborPutMetricAlarm(w, params)
	case "DescribeAlarms":
		s.cborDescribeAlarms(w, params)
	case "DescribeAlarmsForMetric":
		s.cborDescribeAlarmsForMetric(w, params)
	case "DeleteAlarms":
		s.cborDeleteAlarms(w, params)
	case "SetAlarmState", "EnableAlarmActions", "DisableAlarmActions":
		s.writeCBOR(w, http.StatusOK, map[string]interface{}{})
	case "ListTagsForResource":
		s.cborListTagsForResource(w, params)
	case "TagResource":
		s.cborTagResource(w, params)
	case "UntagResource":
		s.cborUntagResource(w, params)
	default:
		s.cborError(w, http.StatusBadRequest, "InvalidAction",
			fmt.Sprintf("Action %s is not supported.", action))
	}
}

func (s *Service) cborPutMetricData(w http.ResponseWriter, params map[string]interface{}) {
	ns := mapStr(params, "Namespace")
	if ns == "" {
		s.cborError(w, http.StatusBadRequest, "MissingParameter", "Namespace is required")
		return
	}
	mdRaw, _ := params["MetricData"].([]interface{})

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, item := range mdRaw {
		md, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		name := mapStr(md, "MetricName")
		dims := mapDims(md, "Dimensions")
		val := mapFloat(md, "Value")
		unit := mapStr(md, "Unit")
		if unit == "" {
			unit = "None"
		}
		ts := mapTime(md, "Timestamp")
		if ts.IsZero() {
			ts = time.Now().UTC()
		}
		series := s.findOrCreate(ns, name, dims)
		series.points = append(series.points, dataPoint{timestamp: ts, value: val, unit: unit})
		if len(series.points) > maxPoints {
			series.points = series.points[len(series.points)-maxPoints:]
		}
	}
	s.writeCBOR(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) cborListMetrics(w http.ResponseWriter, params map[string]interface{}) {
	ns := mapStr(params, "Namespace")
	mn := mapStr(params, "MetricName")
	dimFilter := mapDims(params, "Dimensions")

	s.mu.RLock()
	defer s.mu.RUnlock()

	var metrics []interface{}
	for _, seriesList := range s.metrics {
		for _, series := range seriesList {
			if ns != "" && series.namespace != ns {
				continue
			}
			if mn != "" && series.metricName != mn {
				continue
			}
			if !matchDimFilters(series.dimensions, dimFilter) {
				continue
			}
			var dims []interface{}
			for k, v := range series.dimensions {
				dims = append(dims, map[string]interface{}{"Name": k, "Value": v})
			}
			if dims == nil {
				dims = []interface{}{}
			}
			metrics = append(metrics, map[string]interface{}{
				"Namespace":  series.namespace,
				"MetricName": series.metricName,
				"Dimensions": dims,
			})
		}
	}
	if metrics == nil {
		metrics = []interface{}{}
	}
	s.writeCBOR(w, http.StatusOK, map[string]interface{}{
		"Metrics":   metrics,
		"NextToken": "",
	})
}

func (s *Service) cborGetMetricStatistics(w http.ResponseWriter, params map[string]interface{}) {
	ns := mapStr(params, "Namespace")
	mn := mapStr(params, "MetricName")
	dims := mapDims(params, "Dimensions")
	start := mapTime(params, "StartTime")
	end := mapTime(params, "EndTime")
	period := mapInt(params, "Period")
	if period <= 0 {
		period = 60
	}
	statsRaw := mapStrList(params, "Statistics")
	unitFilter := mapStr(params, "Unit")

	s.mu.RLock()
	defer s.mu.RUnlock()

	var datapoints []interface{}
	if series := s.find(ns, mn, dims); series != nil {
		for _, b := range aggregateToBuckets(series.points, start, end, period, unitFilter) {
			dp := map[string]interface{}{
				"Timestamp":   CborEpochTime(b.start.Unix()),
				"SampleCount": b.count,
				"Unit":        b.unit,
			}
			for _, stat := range statsRaw {
				switch stat {
				case "Average":
					dp["Average"] = b.average()
				case "Sum":
					dp["Sum"] = b.sum
				case "Minimum":
					dp["Minimum"] = b.min
				case "Maximum":
					dp["Maximum"] = b.max
				}
			}
			datapoints = append(datapoints, dp)
		}
	}
	if datapoints == nil {
		datapoints = []interface{}{}
	}
	s.writeCBOR(w, http.StatusOK, map[string]interface{}{
		"Label":      mn,
		"Datapoints": datapoints,
	})
}

func (s *Service) cborGetMetricData(w http.ResponseWriter, params map[string]interface{}) {
	start := mapTime(params, "StartTime")
	end := mapTime(params, "EndTime")

	queriesRaw, _ := params["MetricDataQueries"].([]interface{})

	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []interface{}
	for _, qRaw := range queriesRaw {
		q, ok := qRaw.(map[string]interface{})
		if !ok {
			continue
		}
		id := mapStr(q, "Id")
		msRaw, _ := q["MetricStat"].(map[string]interface{})
		metricRaw, _ := msRaw["Metric"].(map[string]interface{})
		qNS := mapStr(metricRaw, "Namespace")
		qMN := mapStr(metricRaw, "MetricName")
		qDims := mapDims(metricRaw, "Dimensions")
		period := mapInt(msRaw, "Period")
		if period <= 0 {
			period = 60
		}
		stat := mapStr(msRaw, "Stat")

		res := map[string]interface{}{
			"Id":         id,
			"Label":      qMN,
			"Timestamps": []interface{}{},
			"Values":     []interface{}{},
			"StatusCode": "Complete",
		}
		if series := s.find(qNS, qMN, qDims); series != nil {
			var ts []interface{}
			var vals []interface{}
			for _, b := range aggregateToBuckets(series.points, start, end, period, "") {
				// Tag-1 epoch, not RFC3339 — the SDK's CBOR deserializer
				// only accepts tagged timestamps (see CLAUDE.md).
				ts = append(ts, CborEpochTime(b.start.Unix()))
				vals = append(vals, b.statValue(stat))
			}
			if ts != nil {
				res["Timestamps"] = ts
				res["Values"] = vals
			}
		}
		results = append(results, res)
	}
	if results == nil {
		results = []interface{}{}
	}
	s.writeCBOR(w, http.StatusOK, map[string]interface{}{
		"MetricDataResults": results,
		"Messages":          []interface{}{},
	})
}

func (s *Service) cborPutMetricAlarm(w http.ResponseWriter, params map[string]interface{}) {
	name := mapStr(params, "AlarmName")
	if name == "" {
		s.cborError(w, http.StatusBadRequest, "MissingParameter", "AlarmName is required")
		return
	}
	dims := mapDims(params, "Dimensions")

	s.mu.Lock()
	s.alarms[name] = &alarm{
		name:               name,
		arn:                s.alarmARN(name),
		namespace:          mapStr(params, "Namespace"),
		metricName:         mapStr(params, "MetricName"),
		comparisonOperator: mapStr(params, "ComparisonOperator"),
		threshold:          mapFloat(params, "Threshold"),
		evaluationPeriods:  mapInt(params, "EvaluationPeriods"),
		period:             mapInt(params, "Period"),
		statistic:          mapStr(params, "Statistic"),
		unit:               mapStr(params, "Unit"),
		description:        mapStr(params, "AlarmDescription"),
		createdAt:          time.Now().UTC(),
		dimensions:         dims,
	}
	// Store tags if provided
	tags := mapKVList(params, "Tags", "Key", "Value")
	if len(tags) > 0 {
		arn := s.alarmARN(name)
		if s.tags[arn] == nil {
			s.tags[arn] = map[string]string{}
		}
		for k, v := range tags {
			s.tags[arn][k] = v
		}
	}
	s.mu.Unlock()

	s.writeCBOR(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) cborDescribeAlarms(w http.ResponseWriter, params map[string]interface{}) {
	alarmNames := mapStrList(params, "AlarmNames")
	stateValue := mapStr(params, "StateValue")

	s.mu.RLock()
	defer s.mu.RUnlock()

	var alarms []interface{}
	for _, a := range s.alarms {
		if len(alarmNames) > 0 && !containsStr(alarmNames, a.name) {
			continue
		}
		if stateValue != "" && stateValue != "OK" {
			continue
		}
		alarms = append(alarms, s.cborAlarmMap(a))
	}
	if alarms == nil {
		alarms = []interface{}{}
	}
	s.writeCBOR(w, http.StatusOK, map[string]interface{}{
		"MetricAlarms":    alarms,
		"CompositeAlarms": []interface{}{},
	})
}

func (s *Service) cborDescribeAlarmsForMetric(w http.ResponseWriter, params map[string]interface{}) {
	ns := mapStr(params, "Namespace")
	mn := mapStr(params, "MetricName")
	dims := mapDims(params, "Dimensions")

	s.mu.RLock()
	defer s.mu.RUnlock()

	var alarms []interface{}
	for _, a := range s.alarms {
		if a.namespace != ns || a.metricName != mn {
			continue
		}
		if !matchDims(a.dimensions, dims) {
			continue
		}
		alarms = append(alarms, s.cborAlarmMap(a))
	}
	if alarms == nil {
		alarms = []interface{}{}
	}
	s.writeCBOR(w, http.StatusOK, map[string]interface{}{"MetricAlarms": alarms})
}

func (s *Service) cborDeleteAlarms(w http.ResponseWriter, params map[string]interface{}) {
	names := mapStrList(params, "AlarmNames")
	s.mu.Lock()
	for _, name := range names {
		delete(s.alarms, name)
	}
	s.mu.Unlock()
	s.writeCBOR(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) cborListTagsForResource(w http.ResponseWriter, params map[string]interface{}) {
	arn := mapStr(params, "ResourceARN")
	s.mu.RLock()
	tags := s.tags[arn]
	s.mu.RUnlock()

	var tagList []interface{}
	for k, v := range tags {
		tagList = append(tagList, map[string]interface{}{"Key": k, "Value": v})
	}
	if tagList == nil {
		tagList = []interface{}{}
	}
	s.writeCBOR(w, http.StatusOK, map[string]interface{}{"Tags": tagList})
}

func (s *Service) cborTagResource(w http.ResponseWriter, params map[string]interface{}) {
	arn := mapStr(params, "ResourceARN")
	tags := mapKVList(params, "Tags", "Key", "Value")
	s.mu.Lock()
	if s.tags[arn] == nil {
		s.tags[arn] = map[string]string{}
	}
	for k, v := range tags {
		s.tags[arn][k] = v
	}
	s.mu.Unlock()
	s.writeCBOR(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) cborUntagResource(w http.ResponseWriter, params map[string]interface{}) {
	arn := mapStr(params, "ResourceARN")
	keys := mapStrList(params, "TagKeys")
	s.mu.Lock()
	if tags, ok := s.tags[arn]; ok {
		for _, k := range keys {
			delete(tags, k)
		}
	}
	s.mu.Unlock()
	s.writeCBOR(w, http.StatusOK, map[string]interface{}{})
}

func (s *Service) cborAlarmMap(a *alarm) map[string]interface{} {
	var dims []interface{}
	for k, v := range a.dimensions {
		dims = append(dims, map[string]interface{}{"Name": k, "Value": v})
	}
	if dims == nil {
		dims = []interface{}{}
	}
	epoch := CborEpochTime(a.createdAt.Unix())
	return map[string]interface{}{
		"AlarmName":                          a.name,
		"AlarmArn":                           a.arn,
		"AlarmDescription":                   a.description,
		"AlarmConfigurationUpdatedTimestamp": epoch,
		"ActionsEnabled":                     true,
		"OKActions":                          []interface{}{},
		"AlarmActions":                       []interface{}{},
		"InsufficientDataActions":            []interface{}{},
		"StateValue":                         "OK",
		"StateReason":                        "Nimbus local emulator — state is always OK",
		"StateUpdatedTimestamp":              epoch,
		"MetricName":                         a.metricName,
		"Namespace":                          a.namespace,
		"Statistic":                          a.statistic,
		"Dimensions":                         dims,
		"Period":                             a.period,
		"EvaluationPeriods":                  a.evaluationPeriods,
		"DatapointsToAlarm":                  a.evaluationPeriods,
		"Threshold":                          a.threshold,
		"ComparisonOperator":                 a.comparisonOperator,
		"TreatMissingData":                   "missing",
	}
}

func (s *Service) writeCBOR(w http.ResponseWriter, status int, v map[string]interface{}) {
	w.Header().Set("Content-Type", "application/cbor")
	w.Header().Set("smithy-protocol", "rpc-v2-cbor")
	w.Header().Set("x-amzn-requestid", uid.New())
	w.WriteHeader(status)
	w.Write(cborEncodeMap(v)) //nolint:errcheck
}

func (s *Service) cborError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/cbor")
	w.Header().Set("smithy-protocol", "rpc-v2-cbor")
	w.Header().Set("x-amzn-requestid", uid.New())
	// Smithy rpc-v2-cbor errors also set X-Amzn-Errortype
	w.Header().Set("X-Amzn-Errortype", code)
	w.WriteHeader(status)
	errMap := map[string]interface{}{"__type": code, "message": msg}
	w.Write(cborEncodeMap(errMap)) //nolint:errcheck
}
