package cloudwatchmetrics

import (
	"fmt"
	"math"
	"time"
)

// ── Minimal CBOR decoder ──────────────────────────────────────────────────────
// Handles the subset of CBOR used by the AWS SDK Go v2 smithy-rpc-v2-cbor
// protocol for CloudWatch Metrics requests.

func cborDecode(data []byte) (map[string]interface{}, error) {
	v, _, err := cborDecodeValue(data, 0)
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("cbor: expected map at root, got %T", v)
	}
	return m, nil
}

func cborDecodeValue(data []byte, off int) (interface{}, int, error) {
	if off >= len(data) {
		return nil, off, fmt.Errorf("cbor: unexpected end of data at offset %d", off)
	}
	b := data[off]
	off++
	major := b >> 5
	info := b & 0x1f

	// Major type 7 (simple/float) must be handled before cborDecodeArg because
	// the "additional bytes" for float16/32/64 are the value itself, not a
	// length argument — cborDecodeArg would double-consume them.
	if major == 7 {
		switch info {
		case 20:
			return false, off, nil
		case 21:
			return true, off, nil
		case 22, 23:
			return nil, off, nil
		case 24: // 1-byte simple value (skip)
			if off >= len(data) {
				return nil, off, fmt.Errorf("cbor: float16 truncated")
			}
			return nil, off + 1, nil
		case 25: // float16 — not used in CloudWatch API; advance and return 0
			if off+2 > len(data) {
				return nil, off, fmt.Errorf("cbor: float16 truncated")
			}
			return 0.0, off + 2, nil
		case 26: // float32
			if off+4 > len(data) {
				return nil, off, fmt.Errorf("cbor: float32 truncated")
			}
			bits := uint32(data[off])<<24 | uint32(data[off+1])<<16 |
				uint32(data[off+2])<<8 | uint32(data[off+3])
			return float64(math.Float32frombits(bits)), off + 4, nil
		case 27: // float64
			if off+8 > len(data) {
				return nil, off, fmt.Errorf("cbor: float64 truncated")
			}
			bits := uint64(data[off])<<56 | uint64(data[off+1])<<48 |
				uint64(data[off+2])<<40 | uint64(data[off+3])<<32 |
				uint64(data[off+4])<<24 | uint64(data[off+5])<<16 |
				uint64(data[off+6])<<8 | uint64(data[off+7])
			return math.Float64frombits(bits), off + 8, nil
		}
		return nil, off, fmt.Errorf("cbor: unsupported simple/float info %d", info)
	}

	// For all other major types, decode the argument (length or value).
	n, off, err := cborDecodeArg(data, off, info)
	if err != nil {
		return nil, off, err
	}

	switch major {
	case 0: // unsigned int
		return n, off, nil
	case 1: // negative int
		return -int64(n) - 1, off, nil
	case 2: // byte string
		end := off + int(n)
		if end > len(data) {
			return nil, off, fmt.Errorf("cbor: byte string overflows data")
		}
		out := make([]byte, n)
		copy(out, data[off:end])
		return out, end, nil
	case 3: // text string
		end := off + int(n)
		if end > len(data) {
			return nil, off, fmt.Errorf("cbor: text string overflows data")
		}
		s := string(data[off:end])
		return s, end, nil
	case 4: // array
		return cborDecodeArray(data, off, n, info == 31)
	case 5: // map
		return cborDecodeMap(data, off, n, info == 31)
	case 6: // tag: n is the tag number, followed by the tagged value
		v, off, err := cborDecodeValue(data, off)
		if err != nil {
			return nil, off, err
		}
		if n == 1 {
			// Tag 1: epoch-based date/time — the wire format for every
			// Smithy Timestamp shape. The SDK encodes fractional epochs as
			// float64.
			switch t := v.(type) {
			case uint64:
				return time.Unix(int64(t), 0).UTC(), off, nil
			case int64:
				return time.Unix(t, 0).UTC(), off, nil
			case float64:
				sec, frac := math.Modf(t)
				return time.Unix(int64(sec), int64(frac*1e9)).UTC(), off, nil
			}
		}
		// Unknown tags: surface the inner value untagged.
		return v, off, nil
	}
	return nil, off, fmt.Errorf("cbor: unsupported major type %d info %d", major, info)
}

// cborDecodeArg returns the numeric argument encoded in the additional info
// byte, advancing off past any following argument bytes. For indefinite length
// (info == 31) it returns 0 and leaves off unchanged.
func cborDecodeArg(data []byte, off int, info byte) (uint64, int, error) {
	switch {
	case info <= 23:
		return uint64(info), off, nil
	case info == 24:
		if off >= len(data) {
			return 0, off, fmt.Errorf("cbor: truncated 1-byte arg")
		}
		return uint64(data[off]), off + 1, nil
	case info == 25:
		if off+2 > len(data) {
			return 0, off, fmt.Errorf("cbor: truncated 2-byte arg")
		}
		return uint64(data[off])<<8 | uint64(data[off+1]), off + 2, nil
	case info == 26:
		if off+4 > len(data) {
			return 0, off, fmt.Errorf("cbor: truncated 4-byte arg")
		}
		n := uint64(data[off])<<24 | uint64(data[off+1])<<16 |
			uint64(data[off+2])<<8 | uint64(data[off+3])
		return n, off + 4, nil
	case info == 27:
		if off+8 > len(data) {
			return 0, off, fmt.Errorf("cbor: truncated 8-byte arg")
		}
		n := uint64(data[off])<<56 | uint64(data[off+1])<<48 |
			uint64(data[off+2])<<40 | uint64(data[off+3])<<32 |
			uint64(data[off+4])<<24 | uint64(data[off+5])<<16 |
			uint64(data[off+6])<<8 | uint64(data[off+7])
		return n, off + 8, nil
	case info == 31:
		return 0, off, nil // indefinite length
	}
	return 0, off, fmt.Errorf("cbor: reserved additional info %d", info)
}

func cborDecodeArray(data []byte, off int, n uint64, indefinite bool) ([]interface{}, int, error) {
	var arr []interface{}
	if indefinite {
		for {
			if off >= len(data) {
				return nil, off, fmt.Errorf("cbor: unterminated indefinite array")
			}
			if data[off] == 0xff {
				return arr, off + 1, nil
			}
			v, newOff, err := cborDecodeValue(data, off)
			if err != nil {
				return nil, newOff, err
			}
			arr = append(arr, v)
			off = newOff
		}
	}
	arr = make([]interface{}, 0, n)
	for i := uint64(0); i < n; i++ {
		v, newOff, err := cborDecodeValue(data, off)
		if err != nil {
			return nil, newOff, err
		}
		arr = append(arr, v)
		off = newOff
	}
	return arr, off, nil
}

func cborDecodeMap(data []byte, off int, n uint64, indefinite bool) (map[string]interface{}, int, error) {
	m := make(map[string]interface{})
	if indefinite {
		for {
			if off >= len(data) {
				return nil, off, fmt.Errorf("cbor: unterminated indefinite map")
			}
			if data[off] == 0xff {
				return m, off + 1, nil
			}
			k, newOff, err := cborDecodeValue(data, off)
			if err != nil {
				return nil, newOff, err
			}
			off = newOff
			v, newOff, err := cborDecodeValue(data, off)
			if err != nil {
				return nil, newOff, err
			}
			off = newOff
			if ks, ok := k.(string); ok {
				m[ks] = v
			}
		}
	}
	for i := uint64(0); i < n; i++ {
		k, newOff, err := cborDecodeValue(data, off)
		if err != nil {
			return nil, newOff, err
		}
		off = newOff
		v, newOff, err := cborDecodeValue(data, off)
		if err != nil {
			return nil, newOff, err
		}
		off = newOff
		if ks, ok := k.(string); ok {
			m[ks] = v
		}
	}
	return m, off, nil
}

// ── Minimal CBOR encoder ──────────────────────────────────────────────────────

// CborEpochTime is a sentinel type for encoding a time.Time as a CBOR tag-1
// epoch-seconds timestamp (the Smithy rpc-v2-cbor wire format for Timestamp
// shapes). Use it as map values when building CBOR alarm/metric responses.
type CborEpochTime int64

// cborEncodeMap encodes a map[string]interface{} to CBOR.
// Values may be: nil, bool, string, int, float64, CborEpochTime, []string,
// []map[string]string, map[string]interface{}, or []interface{}.
func cborEncodeMap(m map[string]interface{}) []byte {
	return cborEncodeValue(m)
}

func cborEncodeValue(v interface{}) []byte {
	switch vv := v.(type) {
	case nil:
		return []byte{0xf6}
	case CborEpochTime:
		// CBOR tag 1 (epoch-based date/time) followed by unsigned integer seconds.
		// 0xc1 = major type 6 (tag) | additional info 1 (tag number 1).
		return append([]byte{0xc1}, cborUint(uint64(vv))...)
	case bool:
		if vv {
			return []byte{0xf5}
		}
		return []byte{0xf4}
	case string:
		return cborText(vv)
	case int:
		return cborUint(uint64(vv))
	case int64:
		if vv < 0 {
			u := uint64(-vv - 1)
			b := cborUintBytes(u)
			b[0] |= 0x20 // major type 1
			return b
		}
		return cborUint(uint64(vv))
	case uint64:
		return cborUint(vv)
	case float64:
		buf := make([]byte, 9)
		buf[0] = 0xfb
		bits := math.Float64bits(vv)
		for i := 0; i < 8; i++ {
			buf[8-i] = byte(bits >> (uint(i) * 8))
		}
		return buf
	case []byte:
		return cborByteString(vv)
	case []interface{}:
		return cborArray(vv)
	case []string:
		b := cborLenHeader(4, uint64(len(vv)))
		for _, s := range vv {
			b = append(b, cborText(s)...)
		}
		return b
	case []map[string]string:
		b := cborLenHeader(4, uint64(len(vv)))
		for _, item := range vv {
			b = append(b, cborStringMap(item)...)
		}
		return b
	case map[string]interface{}:
		b := cborLenHeader(5, uint64(len(vv)))
		for k, val := range vv {
			b = append(b, cborText(k)...)
			b = append(b, cborEncodeValue(val)...)
		}
		return b
	case map[string]string:
		return cborStringMap(vv)
	}
	return []byte{0xf6} // null for unrecognised types
}

func cborText(s string) []byte {
	b := cborLenHeader(3, uint64(len(s)))
	return append(b, s...)
}

func cborByteString(data []byte) []byte {
	b := cborLenHeader(2, uint64(len(data)))
	return append(b, data...)
}

func cborArray(items []interface{}) []byte {
	b := cborLenHeader(4, uint64(len(items)))
	for _, item := range items {
		b = append(b, cborEncodeValue(item)...)
	}
	return b
}

func cborStringMap(m map[string]string) []byte {
	b := cborLenHeader(5, uint64(len(m)))
	for k, v := range m {
		b = append(b, cborText(k)...)
		b = append(b, cborText(v)...)
	}
	return b
}

func cborUint(n uint64) []byte {
	return cborUintBytes(n) // major type 0 already
}

func cborUintBytes(n uint64) []byte {
	return cborLenHeader(0, n)
}

func cborLenHeader(major byte, n uint64) []byte {
	major <<= 5
	switch {
	case n <= 23:
		return []byte{major | byte(n)}
	case n <= 0xff:
		return []byte{major | 24, byte(n)}
	case n <= 0xffff:
		return []byte{major | 25, byte(n >> 8), byte(n)}
	case n <= 0xffffffff:
		return []byte{major | 26, byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}
	default:
		return []byte{major | 27,
			byte(n >> 56), byte(n >> 48), byte(n >> 40), byte(n >> 32),
			byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}
	}
}

// ── Map extraction helpers ────────────────────────────────────────────────────

func mapStr(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func mapFloat(m map[string]interface{}, key string) float64 {
	if v, ok := m[key]; ok {
		switch vv := v.(type) {
		case float64:
			return vv
		case uint64:
			return float64(vv)
		case int64:
			return float64(vv)
		}
	}
	return 0
}

func mapInt(m map[string]interface{}, key string) int {
	return int(mapFloat(m, key))
}

// mapTime extracts a Timestamp shape: a time.Time from the CBOR path (tag 1),
// or an RFC3339 string from JSON-shaped input. Zero time when absent.
func mapTime(m map[string]interface{}, key string) time.Time {
	switch v := m[key].(type) {
	case time.Time:
		return v
	case string:
		t, _ := time.Parse(time.RFC3339, v)
		return t
	}
	return time.Time{}
}

func mapStrList(m map[string]interface{}, key string) []string {
	v, ok := m[key]
	if !ok {
		return nil
	}
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// mapDims extracts a list of Dimensions (or similar key/value pair lists) from
// a CBOR-decoded map, returning a flat map[string]string.
func mapDims(m map[string]interface{}, key string) map[string]string {
	v, ok := m[key]
	if !ok {
		return map[string]string{}
	}
	arr, ok := v.([]interface{})
	if !ok {
		return map[string]string{}
	}
	out := map[string]string{}
	for _, item := range arr {
		if dm, ok := item.(map[string]interface{}); ok {
			name := mapStr(dm, "Name")
			val := mapStr(dm, "Value")
			if name != "" {
				out[name] = val
			}
		}
	}
	return out
}

// mapKVList extracts a list of key/value objects (e.g. Tags) as map[string]string.
func mapKVList(m map[string]interface{}, key, kField, vField string) map[string]string {
	v, ok := m[key]
	if !ok {
		return map[string]string{}
	}
	arr, ok := v.([]interface{})
	if !ok {
		return map[string]string{}
	}
	out := map[string]string{}
	for _, item := range arr {
		if dm, ok := item.(map[string]interface{}); ok {
			k := mapStr(dm, kField)
			val := mapStr(dm, vField)
			if k != "" {
				out[k] = val
			}
		}
	}
	return out
}
