// Package pi emulates the AWS Performance Insights API (service prefix
// PerformanceInsightsv20180227) with synthetic metric data. All responses are
// generated in-memory — no traffic is forwarded to AWS. Metric values follow a
// deterministic sinusoid so repeated queries over the same window return the
// same datapoints. Identifiers are validated against the RDS emulator's
// DbiResourceId / DbClusterResourceId values when a lookup is wired in.
package pi

import (
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

const targetPrefix = "PerformanceInsightsv20180227."

// maxDataPoints caps the number of datapoints per series, mirroring the real
// API's response-size limits.
const maxDataPoints = 1000

// Service implements the Performance Insights emulator. It is stateless —
// data is synthesized per request.
type Service struct {
	// knownResource reports whether a PI resource identifier (e.g. a
	// DbiResourceId from the RDS emulator) exists. Nil accepts everything.
	knownResource func(id string) bool
}

// New creates a PI service. knownResource may be nil to accept any identifier.
func New(knownResource func(id string) bool) *Service {
	return &Service{knownResource: knownResource}
}

func (s *Service) Name() string { return "pi" }

// Reset is a no-op: the service holds no state.
func (s *Service) Reset() {}

// Detect identifies Performance Insights requests by X-Amz-Target header.
func (s *Service) Detect(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	target := r.Header.Get("X-Amz-Target")
	action := target[strings.LastIndex(target, ".")+1:]

	switch action {
	case "GetResourceMetrics":
		s.getResourceMetrics(w, r)
	case "DescribeDimensionKeys":
		s.describeDimensionKeys(w, r)
	case "ListAvailableResourceMetrics":
		s.listAvailableResourceMetrics(w, r)
	case "ListAvailableResourceDimensions":
		s.listAvailableResourceDimensions(w, r)
	case "GetResourceMetadata":
		s.getResourceMetadata(w, r)
	default:
		jsonhttp.Error(w, http.StatusBadRequest, "InvalidArgumentException",
			fmt.Sprintf("Action %s is not supported.", action))
	}
}

// checkIdentifier validates the resource identifier, writing an error response
// and returning false when it is missing or unknown.
func (s *Service) checkIdentifier(w http.ResponseWriter, id string) bool {
	if id == "" {
		jsonhttp.Error(w, http.StatusBadRequest, "InvalidArgumentException",
			"Identifier is required")
		return false
	}
	if s.knownResource != nil && !s.knownResource(id) {
		jsonhttp.Error(w, http.StatusBadRequest, "InvalidArgumentException",
			fmt.Sprintf("Invalid identifier: %s", id))
		return false
	}
	return true
}

// ── Synthetic data generation ─────────────────────────────────────────────────

// loadAt returns the synthetic db.load value at epoch second t: a sinusoid
// with a 1-hour cycle oscillating between 0.1 and 0.9 average active sessions.
func loadAt(t float64) float64 {
	return 0.5 + 0.4*math.Sin(2*math.Pi*t/3600)
}

// dimensionItem is one synthetic dimension group member with its share of the
// total load.
type dimensionItem struct {
	dimensions map[string]string
	weight     float64
}

// dimensionItems returns the synthetic members for a GroupBy group. The
// weights sum to 1 so grouped series add up to the ungrouped load.
func dimensionItems(group string) []dimensionItem {
	switch group {
	case "db.wait_event":
		return []dimensionItem{
			{map[string]string{"db.wait_event.name": "CPU", "db.wait_event.type": "CPU"}, 0.6},
			{map[string]string{"db.wait_event.name": "IO:DataFileRead", "db.wait_event.type": "IO"}, 0.3},
			{map[string]string{"db.wait_event.name": "Lock:transactionid", "db.wait_event.type": "Lock"}, 0.1},
		}
	case "db.sql":
		return []dimensionItem{
			{map[string]string{"db.sql.id": "NIMBUS0000000000000000001", "db.sql.statement": "SELECT * FROM orders WHERE id = $1"}, 0.7},
			{map[string]string{"db.sql.id": "NIMBUS0000000000000000002", "db.sql.statement": "UPDATE orders SET status = $1 WHERE id = $2"}, 0.3},
		}
	case "db.user":
		return []dimensionItem{
			{map[string]string{"db.user.name": "nimbus"}, 1.0},
		}
	case "db.host":
		return []dimensionItem{
			{map[string]string{"db.host.name": "localhost"}, 1.0},
		}
	default:
		return []dimensionItem{
			{map[string]string{group + ".name": "nimbus"}, 1.0},
		}
	}
}

// align clamps and aligns a query window to the period, returning the aligned
// start/end plus the datapoint timestamps (at most maxDataPoints).
func align(start, end float64, period int) (alignedStart, alignedEnd float64, stamps []float64) {
	if period <= 0 {
		period = 60
	}
	p := float64(period)
	alignedStart = math.Floor(start/p) * p
	alignedEnd = math.Ceil(end/p) * p
	if alignedEnd <= alignedStart {
		alignedEnd = alignedStart + p
	}
	for t := alignedStart + p; t <= alignedEnd && len(stamps) < maxDataPoints; t += p {
		stamps = append(stamps, t)
	}
	return alignedStart, alignedEnd, stamps
}

// ── GetResourceMetrics ────────────────────────────────────────────────────────

type groupBy struct {
	Group      string   `json:"Group"`
	Dimensions []string `json:"Dimensions"`
	Limit      int      `json:"Limit"`
}

type metricQuery struct {
	Metric  string            `json:"Metric"`
	GroupBy *groupBy          `json:"GroupBy"`
	Filter  map[string]string `json:"Filter"`
}

type getResourceMetricsRequest struct {
	ServiceType     string        `json:"ServiceType"`
	Identifier      string        `json:"Identifier"`
	MetricQueries   []metricQuery `json:"MetricQueries"`
	StartTime       float64       `json:"StartTime"`
	EndTime         float64       `json:"EndTime"`
	PeriodInSeconds int           `json:"PeriodInSeconds"`
}

type dataPoint struct {
	Timestamp float64 `json:"Timestamp"`
	Value     float64 `json:"Value"`
}

type responseResourceMetricKey struct {
	Metric     string            `json:"Metric"`
	Dimensions map[string]string `json:"Dimensions,omitempty"`
}

type metricKeyDataPoints struct {
	Key        responseResourceMetricKey `json:"Key"`
	DataPoints []dataPoint               `json:"DataPoints"`
}

func (s *Service) getResourceMetrics(w http.ResponseWriter, r *http.Request) {
	req, ok := jsonhttp.Decode[getResourceMetricsRequest](w, r)
	if !ok {
		return
	}
	if !s.checkIdentifier(w, req.Identifier) {
		return
	}
	if len(req.MetricQueries) == 0 {
		jsonhttp.Error(w, http.StatusBadRequest, "InvalidArgumentException",
			"MetricQueries is required")
		return
	}

	alignedStart, alignedEnd, stamps := align(req.StartTime, req.EndTime, req.PeriodInSeconds)

	metricList := []metricKeyDataPoints{}
	for _, q := range req.MetricQueries {
		if q.GroupBy == nil {
			series := metricKeyDataPoints{
				Key:        responseResourceMetricKey{Metric: q.Metric},
				DataPoints: seriesPoints(stamps, 1.0),
			}
			metricList = append(metricList, series)
			continue
		}
		items := dimensionItems(q.GroupBy.Group)
		if q.GroupBy.Limit > 0 && q.GroupBy.Limit < len(items) {
			items = items[:q.GroupBy.Limit]
		}
		for _, item := range items {
			metricList = append(metricList, metricKeyDataPoints{
				Key: responseResourceMetricKey{
					Metric:     q.Metric,
					Dimensions: filterDimensions(item.dimensions, q.GroupBy.Dimensions),
				},
				DataPoints: seriesPoints(stamps, item.weight),
			})
		}
	}

	jsonhttp.Write(w, http.StatusOK, map[string]any{
		"AlignedStartTime": alignedStart,
		"AlignedEndTime":   alignedEnd,
		"Identifier":       req.Identifier,
		"MetricList":       metricList,
	})
}

func seriesPoints(stamps []float64, weight float64) []dataPoint {
	points := make([]dataPoint, len(stamps))
	for i, t := range stamps {
		points[i] = dataPoint{Timestamp: t, Value: round3(weight * loadAt(t))}
	}
	return points
}

// filterDimensions keeps only the requested dimension names; an empty request
// keeps everything.
func filterDimensions(dims map[string]string, keep []string) map[string]string {
	if len(keep) == 0 {
		return dims
	}
	out := map[string]string{}
	for _, k := range keep {
		if v, ok := dims[k]; ok {
			out[k] = v
		}
	}
	return out
}

func round3(v float64) float64 {
	return math.Round(v*1000) / 1000
}

// ── DescribeDimensionKeys ─────────────────────────────────────────────────────

type describeDimensionKeysRequest struct {
	ServiceType     string   `json:"ServiceType"`
	Identifier      string   `json:"Identifier"`
	StartTime       float64  `json:"StartTime"`
	EndTime         float64  `json:"EndTime"`
	Metric          string   `json:"Metric"`
	PeriodInSeconds int      `json:"PeriodInSeconds"`
	GroupBy         *groupBy `json:"GroupBy"`
}

func (s *Service) describeDimensionKeys(w http.ResponseWriter, r *http.Request) {
	req, ok := jsonhttp.Decode[describeDimensionKeysRequest](w, r)
	if !ok {
		return
	}
	if !s.checkIdentifier(w, req.Identifier) {
		return
	}
	if req.GroupBy == nil || req.GroupBy.Group == "" {
		jsonhttp.Error(w, http.StatusBadRequest, "InvalidArgumentException",
			"GroupBy.Group is required")
		return
	}

	alignedStart, alignedEnd, stamps := align(req.StartTime, req.EndTime, req.PeriodInSeconds)
	var total float64
	for _, t := range stamps {
		total += loadAt(t)
	}

	items := dimensionItems(req.GroupBy.Group)
	if req.GroupBy.Limit > 0 && req.GroupBy.Limit < len(items) {
		items = items[:req.GroupBy.Limit]
	}
	keys := make([]map[string]any, len(items))
	for i, item := range items {
		keys[i] = map[string]any{
			"Dimensions": filterDimensions(item.dimensions, req.GroupBy.Dimensions),
			"Total":      round3(item.weight * total),
		}
	}

	jsonhttp.Write(w, http.StatusOK, map[string]any{
		"AlignedStartTime": alignedStart,
		"AlignedEndTime":   alignedEnd,
		"Keys":             keys,
	})
}

// ── Metric / dimension catalogs ───────────────────────────────────────────────

type catalogMetric struct {
	Metric      string `json:"Metric"`
	Description string `json:"Description"`
	Unit        string `json:"Unit"`
}

var metricCatalog = []catalogMetric{
	{"db.load.avg", "Average active sessions", "Active Sessions"},
	{"db.sampledload.avg", "Sampled average active sessions", "Active Sessions"},
	{"os.cpuUtilization.total.avg", "Total CPU utilization", "Percent"},
	{"os.memory.free.avg", "Free memory", "Kilobytes"},
}

type listAvailableResourceMetricsRequest struct {
	ServiceType string   `json:"ServiceType"`
	Identifier  string   `json:"Identifier"`
	MetricTypes []string `json:"MetricTypes"`
}

func (s *Service) listAvailableResourceMetrics(w http.ResponseWriter, r *http.Request) {
	req, ok := jsonhttp.Decode[listAvailableResourceMetricsRequest](w, r)
	if !ok {
		return
	}
	if !s.checkIdentifier(w, req.Identifier) {
		return
	}

	metrics := []catalogMetric{}
	for _, m := range metricCatalog {
		if len(req.MetricTypes) == 0 {
			metrics = append(metrics, m)
			continue
		}
		for _, prefix := range req.MetricTypes {
			if strings.HasPrefix(m.Metric, prefix) {
				metrics = append(metrics, m)
				break
			}
		}
	}
	jsonhttp.Write(w, http.StatusOK, map[string]any{"Metrics": metrics})
}

type listAvailableResourceDimensionsRequest struct {
	ServiceType string   `json:"ServiceType"`
	Identifier  string   `json:"Identifier"`
	Metrics     []string `json:"Metrics"`
}

func (s *Service) listAvailableResourceDimensions(w http.ResponseWriter, r *http.Request) {
	req, ok := jsonhttp.Decode[listAvailableResourceDimensionsRequest](w, r)
	if !ok {
		return
	}
	if !s.checkIdentifier(w, req.Identifier) {
		return
	}

	groups := []map[string]any{
		{"Group": "db.wait_event", "Dimensions": []map[string]string{
			{"Identifier": "db.wait_event.name"}, {"Identifier": "db.wait_event.type"},
		}},
		{"Group": "db.sql", "Dimensions": []map[string]string{
			{"Identifier": "db.sql.id"}, {"Identifier": "db.sql.statement"},
		}},
		{"Group": "db.user", "Dimensions": []map[string]string{
			{"Identifier": "db.user.name"},
		}},
		{"Group": "db.host", "Dimensions": []map[string]string{
			{"Identifier": "db.host.name"},
		}},
	}
	dims := make([]map[string]any, len(req.Metrics))
	for i, m := range req.Metrics {
		dims[i] = map[string]any{"Metric": m, "Groups": groups}
	}
	jsonhttp.Write(w, http.StatusOK, map[string]any{"MetricDimensions": dims})
}

// ── GetResourceMetadata ───────────────────────────────────────────────────────

type getResourceMetadataRequest struct {
	ServiceType string `json:"ServiceType"`
	Identifier  string `json:"Identifier"`
}

func (s *Service) getResourceMetadata(w http.ResponseWriter, r *http.Request) {
	req, ok := jsonhttp.Decode[getResourceMetadataRequest](w, r)
	if !ok {
		return
	}
	if !s.checkIdentifier(w, req.Identifier) {
		return
	}
	jsonhttp.Write(w, http.StatusOK, map[string]any{
		"Identifier": req.Identifier,
		"Features": map[string]any{
			"SQL_DIGEST": map[string]string{"Status": "ENABLED"},
		},
	})
}
