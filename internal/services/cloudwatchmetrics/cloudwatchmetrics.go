// Package cloudwatchmetrics emulates the AWS CloudWatch Metrics API.
// All metric time-series and alarms are stored in-memory. PutMetricData
// accepts any namespace/metric/dimension combination and keeps the most
// recent 10,000 points per series. Alarms are structural only — state is
// always OK and no evaluation logic runs.
package cloudwatchmetrics

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nimbus-local/nimbus/internal/uid"
)

const (
	accountID = "000000000000"
	cwNS      = "https://monitoring.amazonaws.com/doc/2010-08-01/"
	cwVersion = "2010-08-01"
	maxPoints = 10_000
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

func (s *Service) Detect(r *http.Request) bool {
	if !strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		return false
	}
	_ = r.ParseForm()
	return r.FormValue("Version") == cwVersion
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		cwError(w, http.StatusBadRequest, "InvalidParameterValue", "cannot parse request body")
		return
	}
	switch r.FormValue("Action") {
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
	case "SetAlarmState":
		writeXML(w, http.StatusOK, wrap("SetAlarmState", ""))
	case "EnableAlarmActions", "DisableAlarmActions":
		writeXML(w, http.StatusOK, wrap(r.FormValue("Action"), ""))
	case "ListTagsForResource":
		s.listTagsForResource(w, r)
	case "TagResource":
		s.tagResource(w, r)
	case "UntagResource":
		writeXML(w, http.StatusOK, wrap("UntagResource", ""))
	default:
		cwError(w, http.StatusBadRequest, "InvalidAction",
			fmt.Sprintf("Action %s is not supported.", r.FormValue("Action")))
	}
}

// ── PutMetricData ─────────────────────────────────────────────────────────────

func (s *Service) putMetricData(w http.ResponseWriter, r *http.Request) {
	namespace := r.FormValue("Namespace")
	if namespace == "" {
		cwError(w, http.StatusBadRequest, "MissingParameter", "Namespace is required")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for i := 1; ; i++ {
		prefix := fmt.Sprintf("MetricData.member.%d.", i)
		metricName := r.FormValue(prefix + "MetricName")
		if metricName == "" {
			break
		}

		dims := parseDimensions(r, prefix+"Dimensions.member.")
		value, _ := strconv.ParseFloat(r.FormValue(prefix+"Value"), 64)

		tsStr := r.FormValue(prefix + "Timestamp")
		var ts time.Time
		if tsStr != "" {
			ts, _ = time.Parse(time.RFC3339, tsStr)
		}
		if ts.IsZero() {
			ts = time.Now().UTC()
		}

		unit := r.FormValue(prefix + "Unit")
		if unit == "" {
			unit = "None"
		}

		series := s.findOrCreate(namespace, metricName, dims)
		series.points = append(series.points, dataPoint{timestamp: ts, value: value, unit: unit})
		if len(series.points) > maxPoints {
			series.points = series.points[len(series.points)-maxPoints:]
		}
	}

	writeXML(w, http.StatusOK, wrap("PutMetricData", ""))
}

// ── ListMetrics ───────────────────────────────────────────────────────────────

func (s *Service) listMetrics(w http.ResponseWriter, r *http.Request) {
	nsFilter := r.FormValue("Namespace")
	nameFilter := r.FormValue("MetricName")
	dimFilter := parseDimensions(r, "Dimensions.member.")

	s.mu.RLock()
	defer s.mu.RUnlock()

	var items []string
	for _, seriesList := range s.metrics {
		for _, series := range seriesList {
			if nsFilter != "" && series.namespace != nsFilter {
				continue
			}
			if nameFilter != "" && series.metricName != nameFilter {
				continue
			}
			if !matchDims(series.dimensions, dimFilter) {
				continue
			}
			items = append(items, metricXML(series))
		}
	}

	writeXML(w, http.StatusOK, wrap("ListMetrics", fmt.Sprintf(`
  <ListMetricsResult>
    <Metrics>%s</Metrics>
  </ListMetricsResult>`, strings.Join(items, ""))))
}

// ── GetMetricStatistics ───────────────────────────────────────────────────────

func (s *Service) getMetricStatistics(w http.ResponseWriter, r *http.Request) {
	namespace := r.FormValue("Namespace")
	metricName := r.FormValue("MetricName")
	dims := parseDimensions(r, "Dimensions.member.")

	startStr := r.FormValue("StartTime")
	endStr := r.FormValue("EndTime")
	periodStr := r.FormValue("Period")

	var start, end time.Time
	start, _ = time.Parse(time.RFC3339, startStr)
	end, _ = time.Parse(time.RFC3339, endStr)
	period, _ := strconv.Atoi(periodStr)
	if period <= 0 {
		period = 60
	}

	// Collect requested stats
	var wantedStats []string
	for i := 1; ; i++ {
		s := r.FormValue(fmt.Sprintf("Statistics.member.%d", i))
		if s == "" {
			break
		}
		wantedStats = append(wantedStats, s)
	}
	unit := r.FormValue("Unit")

	s.mu.RLock()
	defer s.mu.RUnlock()

	series := s.find(namespace, metricName, dims)
	if series == nil {
		writeXML(w, http.StatusOK, wrap("GetMetricStatistics", `
  <GetMetricStatisticsResult>
    <Label>`+metricName+`</Label>
    <Datapoints/>
  </GetMetricStatisticsResult>`))
		return
	}

	buckets := aggregateToBuckets(series.points, start, end, period, unit)
	var dpXML []string
	for _, b := range buckets {
		dpXML = append(dpXML, b.toXML(wantedStats))
	}

	writeXML(w, http.StatusOK, wrap("GetMetricStatistics", fmt.Sprintf(`
  <GetMetricStatisticsResult>
    <Label>%s</Label>
    <Datapoints>%s</Datapoints>
  </GetMetricStatisticsResult>`, metricName, strings.Join(dpXML, ""))))
}

// ── GetMetricData ─────────────────────────────────────────────────────────────

func (s *Service) getMetricData(w http.ResponseWriter, r *http.Request) {
	startStr := r.FormValue("StartTime")
	endStr := r.FormValue("EndTime")
	var start, end time.Time
	start, _ = time.Parse(time.RFC3339, startStr)
	end, _ = time.Parse(time.RFC3339, endStr)

	s.mu.RLock()
	defer s.mu.RUnlock()

	var resultXML []string
	for i := 1; ; i++ {
		qPrefix := fmt.Sprintf("MetricDataQueries.member.%d.", i)
		queryID := r.FormValue(qPrefix + "Id")
		if queryID == "" {
			break
		}

		namespace := r.FormValue(qPrefix + "MetricStat.Metric.Namespace")
		metricName := r.FormValue(qPrefix + "MetricStat.Metric.MetricName")
		dims := parseDimensions(r, qPrefix+"MetricStat.Metric.Dimensions.member.")
		periodStr := r.FormValue(qPrefix + "MetricStat.Period")
		stat := r.FormValue(qPrefix + "MetricStat.Stat")
		period, _ := strconv.Atoi(periodStr)
		if period <= 0 {
			period = 60
		}

		series := s.find(namespace, metricName, dims)
		var timestamps, values []string
		if series != nil {
			buckets := aggregateToBuckets(series.points, start, end, period, "")
			for _, b := range buckets {
				v := b.statValue(stat)
				timestamps = append(timestamps, b.start.UTC().Format(time.RFC3339))
				values = append(values, strconv.FormatFloat(v, 'f', -1, 64))
			}
		}

		var tsXML, valXML []string
		for _, t := range timestamps {
			tsXML = append(tsXML, "<member>"+t+"</member>")
		}
		for _, v := range values {
			valXML = append(valXML, "<member>"+v+"</member>")
		}

		resultXML = append(resultXML, fmt.Sprintf(`
    <member>
      <Id>%s</Id>
      <Label>%s</Label>
      <StatusCode>Complete</StatusCode>
      <Timestamps>%s</Timestamps>
      <Values>%s</Values>
    </member>`, queryID, metricName, strings.Join(tsXML, ""), strings.Join(valXML, "")))
	}

	writeXML(w, http.StatusOK, wrap("GetMetricData", fmt.Sprintf(`
  <GetMetricDataResult>
    <MetricDataResults>%s</MetricDataResults>
  </GetMetricDataResult>`, strings.Join(resultXML, ""))))
}

// ── Alarms ────────────────────────────────────────────────────────────────────

func (s *Service) putMetricAlarm(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("AlarmName")
	if name == "" {
		cwError(w, http.StatusBadRequest, "MissingParameter", "AlarmName is required")
		return
	}
	threshold, _ := strconv.ParseFloat(r.FormValue("Threshold"), 64)
	evalPeriods, _ := strconv.Atoi(r.FormValue("EvaluationPeriods"))
	period, _ := strconv.Atoi(r.FormValue("Period"))
	dims := parseDimensions(r, "Dimensions.member.")
	arn := s.alarmARN(name)

	s.mu.Lock()
	s.alarms[name] = &alarm{
		name:               name,
		arn:                arn,
		namespace:          r.FormValue("Namespace"),
		metricName:         r.FormValue("MetricName"),
		comparisonOperator: r.FormValue("ComparisonOperator"),
		threshold:          threshold,
		evaluationPeriods:  evalPeriods,
		period:             period,
		statistic:          r.FormValue("Statistic"),
		unit:               r.FormValue("Unit"),
		description:        r.FormValue("AlarmDescription"),
		createdAt:          time.Now().UTC(),
		dimensions:         dims,
	}
	s.mu.Unlock()

	writeXML(w, http.StatusOK, wrap("PutMetricAlarm", ""))
}

func (s *Service) describeAlarms(w http.ResponseWriter, r *http.Request) {
	stateFilter := r.FormValue("StateValue") // OK | ALARM | INSUFFICIENT_DATA

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Collect requested alarm names (optional filter)
	var nameFilter []string
	for i := 1; ; i++ {
		n := r.FormValue(fmt.Sprintf("AlarmNames.member.%d", i))
		if n == "" {
			break
		}
		nameFilter = append(nameFilter, n)
	}

	var items []string
	for _, a := range s.alarms {
		if len(nameFilter) > 0 && !containsStr(nameFilter, a.name) {
			continue
		}
		if stateFilter != "" && stateFilter != "OK" {
			continue // all alarms are always OK
		}
		items = append(items, s.alarmXML(a))
	}

	writeXML(w, http.StatusOK, wrap("DescribeAlarms", fmt.Sprintf(`
  <DescribeAlarmsResult>
    <MetricAlarms>%s</MetricAlarms>
  </DescribeAlarmsResult>`, strings.Join(items, ""))))
}

func (s *Service) describeAlarmsForMetric(w http.ResponseWriter, r *http.Request) {
	namespace := r.FormValue("Namespace")
	metricName := r.FormValue("MetricName")
	dims := parseDimensions(r, "Dimensions.member.")

	s.mu.RLock()
	defer s.mu.RUnlock()

	var items []string
	for _, a := range s.alarms {
		if a.namespace != namespace || a.metricName != metricName {
			continue
		}
		if !matchDims(a.dimensions, dims) {
			continue
		}
		items = append(items, s.alarmXML(a))
	}

	writeXML(w, http.StatusOK, wrap("DescribeAlarmsForMetric", fmt.Sprintf(`
  <DescribeAlarmsForMetricResult>
    <MetricAlarms>%s</MetricAlarms>
  </DescribeAlarmsForMetricResult>`, strings.Join(items, ""))))
}

func (s *Service) deleteAlarms(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	for i := 1; ; i++ {
		name := r.FormValue(fmt.Sprintf("AlarmNames.member.%d", i))
		if name == "" {
			break
		}
		delete(s.alarms, name)
	}
	s.mu.Unlock()
	writeXML(w, http.StatusOK, wrap("DeleteAlarms", ""))
}

func (s *Service) alarmXML(a *alarm) string {
	var dimXML []string
	for k, v := range a.dimensions {
		dimXML = append(dimXML, fmt.Sprintf(`<member><Name>%s</Name><Value>%s</Value></member>`, k, v))
	}
	return fmt.Sprintf(`
    <member>
      <AlarmName>%s</AlarmName>
      <AlarmArn>%s</AlarmArn>
      <AlarmDescription>%s</AlarmDescription>
      <Namespace>%s</Namespace>
      <MetricName>%s</MetricName>
      <ComparisonOperator>%s</ComparisonOperator>
      <Threshold>%g</Threshold>
      <EvaluationPeriods>%d</EvaluationPeriods>
      <Period>%d</Period>
      <Statistic>%s</Statistic>
      <StateValue>OK</StateValue>
      <StateReason>Nimbus local emulator — state is always OK</StateReason>
      <StateUpdatedTimestamp>%s</StateUpdatedTimestamp>
      <AlarmConfigurationUpdatedTimestamp>%s</AlarmConfigurationUpdatedTimestamp>
      <ActionsEnabled>true</ActionsEnabled>
      <OKActions/>
      <AlarmActions/>
      <InsufficientDataActions/>
      <Dimensions>%s</Dimensions>
    </member>`,
		a.name, a.arn, a.description, a.namespace, a.metricName,
		a.comparisonOperator, a.threshold, a.evaluationPeriods, a.period, a.statistic,
		a.createdAt.UTC().Format(time.RFC3339), a.createdAt.UTC().Format(time.RFC3339),
		strings.Join(dimXML, ""))
}

func (s *Service) alarmARN(name string) string {
	return fmt.Sprintf("arn:aws:cloudwatch:%s:%s:alarm:%s", s.region, accountID, name)
}

// ── Tags ──────────────────────────────────────────────────────────────────────

func (s *Service) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("ResourceARN")
	s.mu.RLock()
	tags := s.tags[arn]
	s.mu.RUnlock()

	var items []string
	for k, v := range tags {
		items = append(items, fmt.Sprintf(`<member><Key>%s</Key><Value>%s</Value></member>`, k, v))
	}
	writeXML(w, http.StatusOK, wrap("ListTagsForResource", fmt.Sprintf(`
  <ListTagsForResourceResult>
    <Tags>%s</Tags>
  </ListTagsForResourceResult>`, strings.Join(items, ""))))
}

func (s *Service) tagResource(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("ResourceARN")
	s.mu.Lock()
	if s.tags[arn] == nil {
		s.tags[arn] = map[string]string{}
	}
	for i := 1; ; i++ {
		k := r.FormValue(fmt.Sprintf("Tags.member.%d.Key", i))
		if k == "" {
			break
		}
		s.tags[arn][k] = r.FormValue(fmt.Sprintf("Tags.member.%d.Value", i))
	}
	s.mu.Unlock()
	writeXML(w, http.StatusOK, wrap("TagResource", ""))
}

// ── Inspection endpoint ───────────────────────────────────────────────────────

// MetricsHandler serves /_nimbus/metrics.
func (s *Service) MetricsHandler(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"metrics":[`)
	first := true
	for _, seriesList := range s.metrics {
		for _, series := range seriesList {
			if !first {
				fmt.Fprint(w, ",")
			}
			fmt.Fprintf(w, `{"namespace":%q,"metricName":%q,"points":%d}`,
				series.namespace, series.metricName, len(series.points))
			first = false
		}
	}
	fmt.Fprint(w, `],"alarms":[`)
	first = true
	for _, a := range s.alarms {
		if !first {
			fmt.Fprint(w, ",")
		}
		fmt.Fprintf(w, `{"name":%q,"arn":%q,"state":"OK"}`, a.name, a.arn)
		first = false
	}
	fmt.Fprint(w, `]}`)
}

// ── Storage helpers ───────────────────────────────────────────────────────────

func (s *Service) findOrCreate(namespace, metricName string, dims map[string]string) *metricSeries {
	key := namespace + "/" + metricName
	for _, series := range s.metrics[key] {
		if dimsEqual(series.dimensions, dims) {
			return series
		}
	}
	series := &metricSeries{
		namespace:  namespace,
		metricName: metricName,
		dimensions: dims,
	}
	s.metrics[key] = append(s.metrics[key], series)
	return series
}

func (s *Service) find(namespace, metricName string, dims map[string]string) *metricSeries {
	key := namespace + "/" + metricName
	for _, series := range s.metrics[key] {
		if matchDims(series.dimensions, dims) {
			return series
		}
	}
	return nil
}

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

// matchDims checks that all dims in filter are present in target.
func matchDims(target, filter map[string]string) bool {
	for k, v := range filter {
		if target[k] != v {
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

func (b *bucket) toXML(stats []string) string {
	var parts []string
	for _, stat := range stats {
		parts = append(parts, fmt.Sprintf("<%s>%g</%s>", stat, b.statValue(stat), stat))
	}
	if len(stats) == 0 {
		parts = append(parts, fmt.Sprintf("<Average>%g</Average>", b.average()))
	}
	return fmt.Sprintf(`
    <member>
      <Timestamp>%s</Timestamp>
      <SampleCount>%g</SampleCount>
      <Unit>%s</Unit>
      %s
    </member>`, b.start.UTC().Format(time.RFC3339), b.count, b.unit, strings.Join(parts, ""))
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
		if unitFilter != "" && p.unit != unitFilter && unitFilter != "None" {
			continue
		}
		slot := p.timestamp.Truncate(dur).Unix()
		b := bucketMap[slot]
		if b == nil {
			b = &bucket{
				start: p.timestamp.Truncate(dur),
				min:   math.MaxFloat64,
				max:   -math.MaxFloat64,
				unit:  p.unit,
			}
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

// ── Form parsing helpers ──────────────────────────────────────────────────────

func parseDimensions(r *http.Request, prefix string) map[string]string {
	dims := map[string]string{}
	for i := 1; ; i++ {
		name := r.FormValue(fmt.Sprintf("%s%d.Name", prefix, i))
		if name == "" {
			break
		}
		dims[name] = r.FormValue(fmt.Sprintf("%s%d.Value", prefix, i))
	}
	return dims
}

func metricXML(series *metricSeries) string {
	var dimXML []string
	for k, v := range series.dimensions {
		dimXML = append(dimXML, fmt.Sprintf(`<member><Name>%s</Name><Value>%s</Value></member>`, k, v))
	}
	return fmt.Sprintf(`
    <member>
      <Namespace>%s</Namespace>
      <MetricName>%s</MetricName>
      <Dimensions>%s</Dimensions>
    </member>`, series.namespace, series.metricName, strings.Join(dimXML, ""))
}

func containsStr(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// ── XML helpers ───────────────────────────────────────────────────────────────

func wrap(action, body string) string {
	return fmt.Sprintf(`<%sResponse xmlns=%q>%s<ResponseMetadata><RequestId>%s</RequestId></ResponseMetadata></%sResponse>`,
		action, cwNS, body, uid.New(), action)
}

func writeXML(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(status)
	fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>`)
	fmt.Fprint(w, body)
}

func cwError(w http.ResponseWriter, status int, code, msg string) {
	writeXML(w, status, fmt.Sprintf(
		`<ErrorResponse xmlns=%q><Error><Type>Sender</Type><Code>%s</Code><Message>%s</Message></Error><RequestId>%s</RequestId></ErrorResponse>`,
		cwNS, code, msg, uid.New()))
}
