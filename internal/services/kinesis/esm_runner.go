package kinesis

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ESMInfo is a snapshot of a Lambda event source mapping targeting a Kinesis stream.
type ESMInfo struct {
	UUID             string
	FunctionName     string
	EventSourceArn   string
	BatchSize        int
	StartingPosition string
}

type esmState struct {
	mu      sync.Mutex
	offsets map[string]int // "esmUUID:shardID" -> next unread index in shard.records
}

// StartESMRunner starts a background goroutine that polls Kinesis streams
// for active Lambda event source mappings and invokes Lambda with record batches.
// getESMs is called every second to pick up newly created or deleted mappings.
func (s *Service) StartESMRunner(getESMs func() []ESMInfo, baseURL string) {
	go s.esmLoop(getESMs, baseURL)
}

func (s *Service) esmLoop(getESMs func() []ESMInfo, baseURL string) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	st := &esmState{offsets: map[string]int{}}
	for range ticker.C {
		for _, esm := range getESMs() {
			s.pollESM(esm, st, baseURL)
		}
	}
}

func (s *Service) pollESM(esm ESMInfo, st *esmState, baseURL string) {
	streamName := esmStreamName(esm.EventSourceArn)
	if streamName == "" {
		return
	}

	s.mu.RLock()
	stream, ok := s.streams[streamName]
	s.mu.RUnlock()
	if !ok {
		return
	}

	batchSize := esm.BatchSize
	if batchSize <= 0 {
		batchSize = 100
	}

	for _, sh := range stream.shards {
		key := esm.UUID + ":" + sh.id

		// Snapshot current length for LATEST initialization (no lock overlap with st).
		sh.mu.Lock()
		total := len(sh.records)
		sh.mu.Unlock()

		// Resolve offset (initialize on first encounter).
		st.mu.Lock()
		offset, seen := st.offsets[key]
		if !seen {
			if esm.StartingPosition == "LATEST" {
				offset = total
			}
			// TRIM_HORIZON and default start at 0.
		}
		st.mu.Unlock()

		// Read a batch from the shard (shard lock only, no st lock).
		sh.mu.Lock()
		if offset > len(sh.records) {
			// Ring-buffer wrap: oldest records were evicted, cap to current length.
			offset = len(sh.records)
		}
		end := offset + batchSize
		if end > len(sh.records) {
			end = len(sh.records)
		}
		var batch []record
		if end > offset {
			batch = make([]record, end-offset)
			copy(batch, sh.records[offset:end])
		}
		newOffset := end
		sh.mu.Unlock()

		// Persist updated offset.
		st.mu.Lock()
		st.offsets[key] = newOffset
		st.mu.Unlock()

		if len(batch) == 0 {
			continue
		}

		go s.invokeLambdaKinesis(esm, sh.id, batch, baseURL)
	}
}

func (s *Service) invokeLambdaKinesis(esm ESMInfo, shardID string, batch []record, baseURL string) {
	records := make([]map[string]any, len(batch))
	for i, rec := range batch {
		records[i] = map[string]any{
			"kinesis": map[string]any{
				"kinesisSchemaVersion":        "1.0",
				"partitionKey":                rec.partitionKey,
				"sequenceNumber":              rec.sequenceNumber,
				"data":                        base64.StdEncoding.EncodeToString(rec.data),
				"approximateArrivalTimestamp": float64(rec.arrivalTime.UnixMilli()) / 1000.0,
			},
			"eventSource":       "aws:kinesis",
			"eventVersion":      "1.0",
			"eventID":           fmt.Sprintf("%s:%s", shardID, rec.sequenceNumber),
			"eventName":         "aws:kinesis:record",
			"invokeIdentityArn": fmt.Sprintf("arn:aws:iam::%s:role/nimbus-esm", account),
			"awsRegion":         s.region,
			"eventSourceARN":    esm.EventSourceArn,
		}
	}

	payload, err := json.Marshal(map[string]any{"Records": records})
	if err != nil {
		slog.Warn("kinesis esm: marshal failed", "esm", esm.UUID, "err", err)
		return
	}

	fnName := esmFunctionName(esm.FunctionName)
	url := fmt.Sprintf("%s/2015-03-31/functions/%s/invocations", baseURL, fnName)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		slog.Warn("kinesis esm: build request failed", "esm", esm.UUID, "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Amz-Invocation-Type", "Event")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Warn("kinesis esm: invocation failed", "esm", esm.UUID, "fn", fnName, "err", err)
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) //nolint:errcheck

	slog.Info("kinesis esm: invoked lambda",
		"esm", esm.UUID, "fn", fnName, "shard", shardID,
		"records", len(batch), "status", resp.StatusCode)
}

// esmStreamName extracts the stream name from a Kinesis ARN.
// Format: arn:aws:kinesis:{region}:{account}:stream/{name}
func esmStreamName(arn string) string {
	parts := strings.Split(arn, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-1]
	}
	return ""
}

// esmFunctionName strips qualifier and ARN prefix, returning the bare function name.
func esmFunctionName(s string) string {
	if strings.HasPrefix(s, "arn:") {
		parts := strings.Split(s, ":")
		if len(parts) >= 7 {
			return parts[6]
		}
		return s
	}
	if idx := strings.Index(s, ":"); idx != -1 {
		return s[:idx]
	}
	return s
}
