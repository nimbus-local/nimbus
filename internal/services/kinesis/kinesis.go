package kinesis

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

const account = "000000000000"

// Service implements the AWS Kinesis Data Streams emulator.
// Streams and records are stored in-memory. Each shard holds a ring buffer
// of up to 10 000 records. No actual resharding — MergeShards/SplitShard
// are stubbed to return success.
type Service struct {
	mu      sync.RWMutex
	streams map[string]*stream // name -> stream
	region  string
}

type stream struct {
	name           string
	arn            string
	shardCount     int
	shards         []*shard
	status         string
	createdAt      time.Time
	tags           map[string]string
	retentionHours int
}

type shard struct {
	id       string
	mu       sync.Mutex
	records  []record
	sequence uint64 // monotonically increasing per shard
}

type record struct {
	sequenceNumber string
	partitionKey   string
	data           []byte
	arrivalTime    time.Time
}

const shardRingSize = 10_000

func New(region string) *Service {
	if region == "" {
		region = "us-east-1"
	}
	return &Service{
		streams: map[string]*stream{},
		region:  region,
	}
}

func (s *Service) Name() string { return "kinesis" }

func (s *Service) Detect(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), "Kinesis_20131202.")
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), "Kinesis_20131202.")
	switch op {
	case "CreateStream":
		s.createStream(w, r)
	case "DeleteStream":
		s.deleteStream(w, r)
	case "ListStreams":
		s.listStreams(w, r)
	case "DescribeStream":
		s.describeStream(w, r)
	case "DescribeStreamSummary":
		s.describeStreamSummary(w, r)
	case "ListShards":
		s.listShards(w, r)
	case "AddTagsToStream":
		s.addTagsToStream(w, r)
	case "ListTagsForStream":
		s.listTagsForStream(w, r)
	case "RemoveTagsFromStream":
		s.removeTagsFromStream(w, r)
	case "PutRecord":
		s.putRecord(w, r)
	case "PutRecords":
		s.putRecords(w, r)
	case "GetShardIterator":
		s.getShardIterator(w, r)
	case "GetRecords":
		s.getRecords(w, r)
	case "MergeShards", "SplitShard":
		jsonhttp.Write(w, http.StatusOK, struct{}{})
	case "IncreaseStreamRetentionPeriod", "DecreaseStreamRetentionPeriod":
		s.setRetention(w, r)
	case "EnableEnhancedMonitoring", "DisableEnhancedMonitoring":
		var req struct {
			StreamName string   `json:"StreamName"`
			Metrics    []string `json:"ShardLevelMetrics"`
		}
		decode(w, r, &req)
		jsonhttp.Write(w, http.StatusOK, map[string]any{
			"StreamName":               req.StreamName,
			"CurrentShardLevelMetrics": []string{},
			"DesiredShardLevelMetrics": req.Metrics,
		})
	default:
		jsonhttp.Error(w, http.StatusBadRequest, "UnknownOperationException", "unknown operation: "+op)
	}
}

// --- helpers ---

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		jsonhttp.Error(w, http.StatusBadRequest, "InvalidArgumentException",
			"could not parse request body: "+err.Error())
		return false
	}
	return true
}

func (s *Service) arn(name string) string {
	return fmt.Sprintf("arn:aws:kinesis:%s:%s:stream/%s", s.region, account, name)
}

func (s *Service) getStream(w http.ResponseWriter, name string) (*stream, bool) {
	st, ok := s.streams[name]
	if !ok || st.status == "DELETING" {
		jsonhttp.Error(w, http.StatusBadRequest, "ResourceNotFoundException",
			fmt.Sprintf("Stream %s under account %s not found.", name, account))
		return nil, false
	}
	return st, true
}

func makeShards(count int) []*shard {
	if count <= 0 {
		count = 1
	}
	shards := make([]*shard, count)
	for i := range shards {
		shards[i] = &shard{
			id:      fmt.Sprintf("shardId-%012d", i),
			records: make([]record, 0, 64),
		}
	}
	return shards
}

func shardSummaries(st *stream) []map[string]any {
	n := uint64(len(st.shards))
	out := make([]map[string]any, n)
	for i, sh := range st.shards {
		start := uint64(i) * (^uint64(0) / n)
		var end uint64
		if uint64(i) == n-1 {
			end = ^uint64(0)
		} else {
			end = uint64(i+1) * (^uint64(0) / n)
		}
		out[i] = map[string]any{
			"ShardId": sh.id,
			"HashKeyRange": map[string]string{
				"StartingHashKey": fmt.Sprintf("%d", start),
				"EndingHashKey":   fmt.Sprintf("%d", end),
			},
			"SequenceNumberRange": map[string]string{
				"StartingSequenceNumber": "49000000000000000000000000000000000000000000000000000001",
			},
		}
	}
	return out
}

func streamDescription(st *stream, shards []map[string]any) map[string]any {
	return map[string]any{
		"StreamName":                    st.name,
		"StreamARN":                     st.arn,
		"StreamStatus":                  st.status,
		"Shards":                        shards,
		"HasMoreShards":                 false,
		"RetentionPeriodHours":          st.retentionHours,
		"StreamCreationTimestamp":       st.createdAt.Unix(),
		"EnhancedMonitoring":            []map[string]any{{"ShardLevelMetrics": []string{}}},
		"EncryptionType":                "NONE",
		"KeyId":                         nil,
		"StreamModeDetails":             map[string]string{"StreamMode": "PROVISIONED"},
	}
}

// --- operation handlers ---

func (s *Service) createStream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName string `json:"StreamName"`
		ShardCount int    `json:"ShardCount"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.StreamName == "" {
		jsonhttp.Error(w, http.StatusBadRequest, "InvalidArgumentException", "StreamName is required")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.streams[req.StreamName]; exists {
		jsonhttp.Error(w, http.StatusBadRequest, "ResourceInUseException",
			fmt.Sprintf("Stream %s already exists", req.StreamName))
		return
	}
	s.streams[req.StreamName] = &stream{
		name:           req.StreamName,
		arn:            s.arn(req.StreamName),
		shardCount:     req.ShardCount,
		shards:         makeShards(req.ShardCount),
		status:         "ACTIVE",
		createdAt:      time.Now(),
		tags:           map[string]string{},
		retentionHours: 24,
	}
	jsonhttp.Write(w, http.StatusOK, struct{}{})
}

func (s *Service) deleteStream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName string `json:"StreamName"`
		StreamARN  string `json:"StreamARN"`
	}
	if !decode(w, r, &req) {
		return
	}
	name := s.resolveName(req.StreamName, req.StreamARN)

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.streams[name]; !ok {
		jsonhttp.Error(w, http.StatusBadRequest, "ResourceNotFoundException",
			fmt.Sprintf("Stream %s under account %s not found.", name, account))
		return
	}
	delete(s.streams, name)
	jsonhttp.Write(w, http.StatusOK, struct{}{})
}

func (s *Service) listStreams(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Limit                  int    `json:"Limit"`
		ExclusiveStartStreamName string `json:"ExclusiveStartStreamName"`
	}
	decode(w, r, &req) // optional body

	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.streams))
	for name := range s.streams {
		names = append(names, name)
	}
	jsonhttp.Write(w, http.StatusOK, map[string]any{
		"StreamNames":    names,
		"HasMoreStreams":  false,
	})
}

func (s *Service) describeStream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName string `json:"StreamName"`
		StreamARN  string `json:"StreamARN"`
		Limit      int    `json:"Limit"`
	}
	if !decode(w, r, &req) {
		return
	}
	name := s.resolveName(req.StreamName, req.StreamARN)

	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.getStream(w, name)
	if !ok {
		return
	}
	jsonhttp.Write(w, http.StatusOK, map[string]any{
		"StreamDescription": streamDescription(st, shardSummaries(st)),
	})
}

func (s *Service) describeStreamSummary(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName string `json:"StreamName"`
		StreamARN  string `json:"StreamARN"`
	}
	if !decode(w, r, &req) {
		return
	}
	name := s.resolveName(req.StreamName, req.StreamARN)

	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.getStream(w, name)
	if !ok {
		return
	}
	jsonhttp.Write(w, http.StatusOK, map[string]any{
		"StreamDescriptionSummary": map[string]any{
			"StreamName":              st.name,
			"StreamARN":               st.arn,
			"StreamStatus":            st.status,
			"StreamModeDetails":       map[string]string{"StreamMode": "PROVISIONED"},
			"RetentionPeriodHours":    st.retentionHours,
			"StreamCreationTimestamp": st.createdAt.Unix(),
			"EnhancedMonitoring":      []map[string]any{{"ShardLevelMetrics": []string{}}},
			"EncryptionType":          "NONE",
			"OpenShardCount":          len(st.shards),
			"ConsumerCount":           0,
		},
	})
}

func (s *Service) listShards(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName string `json:"StreamName"`
		StreamARN  string `json:"StreamARN"`
	}
	if !decode(w, r, &req) {
		return
	}
	name := s.resolveName(req.StreamName, req.StreamARN)

	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.getStream(w, name)
	if !ok {
		return
	}
	jsonhttp.Write(w, http.StatusOK, map[string]any{
		"Shards":            shardSummaries(st),
		"NextToken":         nil,
	})
}

func (s *Service) addTagsToStream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName string            `json:"StreamName"`
		StreamARN  string            `json:"StreamARN"`
		Tags       map[string]string `json:"Tags"`
	}
	if !decode(w, r, &req) {
		return
	}
	name := s.resolveName(req.StreamName, req.StreamARN)

	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.getStream(w, name)
	if !ok {
		return
	}
	for k, v := range req.Tags {
		st.tags[k] = v
	}
	jsonhttp.Write(w, http.StatusOK, struct{}{})
}

func (s *Service) listTagsForStream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName string `json:"StreamName"`
		StreamARN  string `json:"StreamARN"`
	}
	if !decode(w, r, &req) {
		return
	}
	name := s.resolveName(req.StreamName, req.StreamARN)

	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.getStream(w, name)
	if !ok {
		return
	}
	tags := make([]map[string]string, 0, len(st.tags))
	for k, v := range st.tags {
		tags = append(tags, map[string]string{"Key": k, "Value": v})
	}
	jsonhttp.Write(w, http.StatusOK, map[string]any{
		"Tags":        tags,
		"HasMoreTags": false,
	})
}

func (s *Service) removeTagsFromStream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName string   `json:"StreamName"`
		StreamARN  string   `json:"StreamARN"`
		TagKeys    []string `json:"TagKeys"`
	}
	if !decode(w, r, &req) {
		return
	}
	name := s.resolveName(req.StreamName, req.StreamARN)

	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.getStream(w, name)
	if !ok {
		return
	}
	for _, k := range req.TagKeys {
		delete(st.tags, k)
	}
	jsonhttp.Write(w, http.StatusOK, struct{}{})
}

func (s *Service) setRetention(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName             string `json:"StreamName"`
		StreamARN              string `json:"StreamARN"`
		RetentionPeriodHours   int    `json:"RetentionPeriodHours"`
	}
	if !decode(w, r, &req) {
		return
	}
	name := s.resolveName(req.StreamName, req.StreamARN)

	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.getStream(w, name)
	if !ok {
		return
	}
	if req.RetentionPeriodHours > 0 {
		st.retentionHours = req.RetentionPeriodHours
	}
	jsonhttp.Write(w, http.StatusOK, struct{}{})
}

// resolveName returns the stream name from either the name or ARN.
func (s *Service) resolveName(name, arn string) string {
	if name != "" {
		return name
	}
	// arn:aws:kinesis:region:account:stream/name
	parts := strings.Split(arn, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-1]
	}
	return ""
}
