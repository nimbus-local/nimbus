package kinesis

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/nimbus-local/nimbus/internal/jsonhttp"
)

// putRecord handles PutRecord — single record to a shard selected by partition key hash.
func (s *Service) putRecord(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName   string `json:"StreamName"`
		StreamARN    string `json:"StreamARN"`
		PartitionKey string `json:"PartitionKey"`
		Data         []byte `json:"Data"`
	}
	if !decode(w, r, &req) {
		return
	}
	name := s.resolveName(req.StreamName, req.StreamARN)

	s.mu.RLock()
	st, ok := s.getStream(w, name)
	s.mu.RUnlock()
	if !ok {
		return
	}

	sh := pickShard(st, req.PartitionKey)
	seq := appendRecord(sh, req.PartitionKey, req.Data)

	jsonhttp.Write(w, http.StatusOK, map[string]any{
		"ShardId":        sh.id,
		"SequenceNumber": seq,
		"EncryptionType": "NONE",
	})
}

// putRecords handles PutRecords — batch of up to 500 records.
func (s *Service) putRecords(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName string `json:"StreamName"`
		StreamARN  string `json:"StreamARN"`
		Records    []struct {
			PartitionKey string `json:"PartitionKey"`
			Data         []byte `json:"Data"`
		} `json:"Records"`
	}
	if !decode(w, r, &req) {
		return
	}
	name := s.resolveName(req.StreamName, req.StreamARN)

	s.mu.RLock()
	st, ok := s.getStream(w, name)
	s.mu.RUnlock()
	if !ok {
		return
	}

	results := make([]map[string]any, len(req.Records))
	for i, rec := range req.Records {
		sh := pickShard(st, rec.PartitionKey)
		seq := appendRecord(sh, rec.PartitionKey, rec.Data)
		results[i] = map[string]any{
			"ShardId":        sh.id,
			"SequenceNumber": seq,
		}
	}
	jsonhttp.Write(w, http.StatusOK, map[string]any{
		"FailedRecordCount": 0,
		"Records":           results,
		"EncryptionType":    "NONE",
	})
}

// pickShard maps a partition key to a shard via MD5 hash mod shardCount,
// matching the AWS algorithm.
func pickShard(st *stream, partitionKey string) *shard {
	h := md5.Sum([]byte(partitionKey))
	var n big.Int
	n.SetBytes(h[:])
	idx := new(big.Int).Mod(&n, big.NewInt(int64(len(st.shards)))).Int64()
	return st.shards[idx]
}

func appendRecord(sh *shard, partitionKey string, data []byte) string {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	sh.sequence++
	seq := fmt.Sprintf("49%019d%019d%019d%06d",
		time.Now().UnixMilli(), 0, sh.sequence, 0)
	rec := record{
		sequenceNumber: seq,
		partitionKey:   partitionKey,
		data:           data,
		arrivalTime:    time.Now(),
	}
	if len(sh.records) >= shardRingSize {
		// drop oldest
		sh.records = sh.records[1:]
	}
	sh.records = append(sh.records, rec)
	return seq
}

// --- iterators ---

// iteratorKey encodes a position in a shard as a base64 JSON blob.
type iteratorKey struct {
	StreamName string `json:"s"`
	ShardID    string `json:"h"`
	// index into shard.records (-1 = LATEST, meaning next write)
	Offset int `json:"o"`
}

func (s *Service) getShardIterator(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamName             string `json:"StreamName"`
		StreamARN              string `json:"StreamARN"`
		ShardId                string `json:"ShardId"`
		ShardIteratorType      string `json:"ShardIteratorType"`
		StartingSequenceNumber string `json:"StartingSequenceNumber"`
		Timestamp              *int64 `json:"Timestamp"`
	}
	if !decode(w, r, &req) {
		return
	}
	name := s.resolveName(req.StreamName, req.StreamARN)

	s.mu.RLock()
	st, ok := s.getStream(w, name)
	s.mu.RUnlock()
	if !ok {
		return
	}

	var sh *shard
	for _, candidate := range st.shards {
		if candidate.id == req.ShardId {
			sh = candidate
			break
		}
	}
	if sh == nil {
		jsonhttp.Error(w, http.StatusBadRequest, "ResourceNotFoundException",
			fmt.Sprintf("Shard %s not found in stream %s", req.ShardId, name))
		return
	}

	sh.mu.Lock()
	offset := len(sh.records) // LATEST: start after all current records
	switch req.ShardIteratorType {
	case "TRIM_HORIZON":
		offset = 0
	case "AT_SEQUENCE_NUMBER":
		offset = findSequenceOffset(sh, req.StartingSequenceNumber, false)
	case "AFTER_SEQUENCE_NUMBER":
		offset = findSequenceOffset(sh, req.StartingSequenceNumber, true)
	case "AT_TIMESTAMP":
		offset = findTimestampOffset(sh, req.Timestamp)
	// LATEST: offset stays at len(records)
	}
	sh.mu.Unlock()

	key := iteratorKey{StreamName: name, ShardID: sh.id, Offset: offset}
	it, _ := json.Marshal(key)
	encoded := base64.StdEncoding.EncodeToString(it)

	jsonhttp.Write(w, http.StatusOK, map[string]string{
		"ShardIterator": encoded,
	})
}

func (s *Service) getRecords(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ShardIterator string `json:"ShardIterator"`
		Limit         int    `json:"Limit"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Limit <= 0 || req.Limit > 10_000 {
		req.Limit = 10_000
	}

	raw, err := base64.StdEncoding.DecodeString(req.ShardIterator)
	if err != nil {
		jsonhttp.Error(w, http.StatusBadRequest, "InvalidArgumentException", "invalid ShardIterator")
		return
	}
	var key iteratorKey
	if err := json.Unmarshal(raw, &key); err != nil {
		jsonhttp.Error(w, http.StatusBadRequest, "InvalidArgumentException", "invalid ShardIterator")
		return
	}

	s.mu.RLock()
	st, ok := s.getStream(w, key.StreamName)
	s.mu.RUnlock()
	if !ok {
		return
	}

	var sh *shard
	for _, candidate := range st.shards {
		if candidate.id == key.ShardID {
			sh = candidate
			break
		}
	}
	if sh == nil {
		jsonhttp.Error(w, http.StatusBadRequest, "ResourceNotFoundException", "shard not found")
		return
	}

	sh.mu.Lock()
	start := key.Offset
	if start < 0 {
		start = 0
	}
	end := start + req.Limit
	if end > len(sh.records) {
		end = len(sh.records)
	}
	slice := sh.records[start:end]
	nextOffset := end
	millisBehind := int64(0)
	if end < len(sh.records) {
		millisBehind = time.Since(sh.records[end-1].arrivalTime).Milliseconds()
	}
	sh.mu.Unlock()

	out := make([]map[string]any, len(slice))
	for i, rec := range slice {
		out[i] = map[string]any{
			"SequenceNumber":              rec.sequenceNumber,
			"ApproximateArrivalTimestamp": rec.arrivalTime.Unix(),
			"Data":                        base64.StdEncoding.EncodeToString(rec.data),
			"PartitionKey":                rec.partitionKey,
			"EncryptionType":              "NONE",
		}
	}

	// build next iterator
	nextKey := iteratorKey{StreamName: key.StreamName, ShardID: key.ShardID, Offset: nextOffset}
	nextRaw, _ := json.Marshal(nextKey)
	nextIt := base64.StdEncoding.EncodeToString(nextRaw)

	jsonhttp.Write(w, http.StatusOK, map[string]any{
		"Records":              out,
		"NextShardIterator":    nextIt,
		"MillisBehindLatest":   millisBehind,
		"ChildShards":          []any{},
	})
}

// findSequenceOffset returns the index of the record matching seq (inclusive or exclusive).
func findSequenceOffset(sh *shard, seq string, after bool) int {
	for i, rec := range sh.records {
		cmp := compareSeq(rec.sequenceNumber, seq)
		if cmp == 0 {
			if after {
				return i + 1
			}
			return i
		}
		if cmp > 0 {
			return i
		}
	}
	return len(sh.records)
}

func findTimestampOffset(sh *shard, tsPtr *int64) int {
	if tsPtr == nil {
		return 0
	}
	ts := time.Unix(*tsPtr, 0)
	for i, rec := range sh.records {
		if !rec.arrivalTime.Before(ts) {
			return i
		}
	}
	return len(sh.records)
}

// compareSeq compares two Kinesis sequence numbers as decimal strings.
func compareSeq(a, b string) int {
	a = strings.TrimLeft(a, "0")
	b = strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		return len(a) - len(b)
	}
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
