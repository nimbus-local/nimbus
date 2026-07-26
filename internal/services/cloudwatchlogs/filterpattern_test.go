package cloudwatchlogs

import "testing"

// perfEvent is a Container Insights performance record, the shape that
// motivated JSON pattern support (issue #109).
const perfEvent = `{"Type":"Task","TaskId":"abc123","ClusterName":"demo-cluster",` +
	`"CpuUtilized":64.5,"Essential":true,"StoppedReason":null,` +
	`"Containers":[{"Name":"app","Image":"nginx:latest"}]}`

func match(t *testing.T, pattern, message string) bool {
	t.Helper()
	m, err := compileFilterPattern(pattern)
	if err != nil {
		t.Fatalf("compile %q: %v", pattern, err)
	}
	if m == nil {
		return true
	}
	return m(message)
}

func TestJSONPattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		message string
		want    bool
	}{
		{"equal string", `{ $.Type = "Task" }`, perfEvent, true},
		{"unequal string", `{ $.Type = "Container" }`, perfEvent, false},
		{"not equal", `{ $.Type != "Container" }`, perfEvent, true},
		{"no spaces", `{$.Type="Task"}`, perfEvent, true},
		{"single quotes", `{ $.Type = 'Task' }`, perfEvent, true},
		{"unquoted value", `{ $.Type = Task }`, perfEvent, true},
		{"nested member", `{ $.Containers[0].Name = "app" }`, perfEvent, true},
		{"array index out of range", `{ $.Containers[1].Name = "app" }`, perfEvent, false},
		{"bracket member name", `{ $["Type"] = "Task" }`, perfEvent, true},

		{"leading wildcard", `{ $.Containers[0].Image = "*:latest" }`, perfEvent, true},
		{"trailing wildcard", `{ $.TaskId = "abc*" }`, perfEvent, true},
		{"inner wildcard", `{ $.ClusterName = "demo*cluster" }`, perfEvent, true},
		{"wildcard no match", `{ $.TaskId = "xyz*" }`, perfEvent, false},

		{"numeric gt", `{ $.CpuUtilized > 50 }`, perfEvent, true},
		{"numeric gte boundary", `{ $.CpuUtilized >= 64.5 }`, perfEvent, true},
		{"numeric lt", `{ $.CpuUtilized < 50 }`, perfEvent, false},
		{"numeric equal", `{ $.CpuUtilized = 64.5 }`, perfEvent, true},
		{"numeric against string field", `{ $.TaskId > 5 }`, perfEvent, false},

		{"is true", `{ $.Essential IS TRUE }`, perfEvent, true},
		{"is false", `{ $.Essential IS FALSE }`, perfEvent, false},
		{"boolean literal", `{ $.Essential = true }`, perfEvent, true},
		{"is null", `{ $.StoppedReason IS NULL }`, perfEvent, true},
		{"is null on present value", `{ $.Type IS NULL }`, perfEvent, false},
		{"not exists", `{ $.Missing NOT EXISTS }`, perfEvent, true},
		{"not exists on present field", `{ $.Type NOT EXISTS }`, perfEvent, false},
		{"missing field never matches", `{ $.Missing != "x" }`, perfEvent, false},

		{"and both true", `{ $.Type = "Task" && $.CpuUtilized > 10 }`, perfEvent, true},
		{"and one false", `{ $.Type = "Task" && $.CpuUtilized > 100 }`, perfEvent, false},
		{"or one true", `{ $.Type = "Container" || $.TaskId = "abc123" }`, perfEvent, true},
		{"or none true", `{ $.Type = "Container" || $.TaskId = "zzz" }`, perfEvent, false},
		{"parentheses", `{ ($.Type = "Container" || $.Type = "Task") && $.Essential IS TRUE }`, perfEvent, true},
		{"and binds tighter than or", `{ $.Type = "Container" && $.Essential IS TRUE || $.TaskId = "abc123" }`, perfEvent, true},

		{"root array selector", `{ $[0].Name = "app" }`, `[{"Name":"app"}]`, true},
		{"plain text never matches", `{ $.Type = "Task" }`, "Type=Task", false},
		{"malformed json never matches", `{ $.Type = "Task" }`, `{"Type":"Task"`, false},
		{"empty pattern matches all", "", perfEvent, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := match(t, tc.pattern, tc.message); got != tc.want {
				t.Errorf("pattern %s against %s = %v, want %v", tc.pattern, tc.message, got, tc.want)
			}
		})
	}
}

func TestJSONPattern_Invalid(t *testing.T) {
	for _, pattern := range []string{
		`{ $.Type = }`,
		`{ $.Type }`,
		`{ }`,
		`{ $.Type = "Task" &&  }`,
		`{ $.Type & $.Other }`,
		`{ $.Type IS MAYBE }`,
		`{ $.Type NOT THERE }`,
		`{ Type = "Task" }`,
		`{ ($.Type = "Task" }`,
		`{ $.Type = "Task" }}extra`,
		`{ $.Type = "unterminated }`,
	} {
		if _, err := compileFilterPattern(pattern); err == nil {
			t.Errorf("expected %s to be rejected", pattern)
		}
	}
}

func TestSpaceDelimitedPattern(t *testing.T) {
	// Fields: 127.0.0.1 | - | frank | 10/Oct/2000:13:55:36 -0700 |
	//         GET /apache_pb.gif HTTP/1.0 | 200 | 2326
	const accessLog = `127.0.0.1 - frank [10/Oct/2000:13:55:36 -0700] "GET /apache_pb.gif HTTP/1.0" 200 2326`

	tests := []struct {
		name    string
		pattern string
		message string
		want    bool
	}{
		{"all placeholders", `[ip, id, user, timestamp, request, status, size]`, accessLog, true},
		{"too few fields declared", `[ip, id, user]`, accessLog, false},
		{"numeric equal", `[ip, id, user, timestamp, request, status = 200, size]`, accessLog, true},
		{"numeric not equal", `[ip, id, user, timestamp, request, status = 404, size]`, accessLog, false},
		{"numeric range", `[ip, id, user, timestamp, request, status >= 200 && status < 300, size]`, accessLog, true},
		{"unquoted wildcard", `[ip, id, user, timestamp, request, status = 2*, size]`, accessLog, true},
		{"wildcard on bracketed field", `[ip, id, user, timestamp = 10/Oct*, request, status, size]`, accessLog, true},
		{"wildcard on quoted field", `[ip, id, user, timestamp, request = "GET*", status, size]`, accessLog, true},
		{"or across values", `[ip, id, user, timestamp, request, status = 404 || status = 200, size]`, accessLog, true},
		{"ellipsis at the front", `[..., status = 200, size]`, accessLog, true},
		{"ellipsis at the front, no match", `[..., status = 500, size]`, accessLog, false},
		{"ellipsis in the middle", `[ip, ..., size = 2326]`, accessLog, true},
		{"ellipsis at the end", `[ip = 127.0.0.1, ...]`, accessLog, true},
		{"too few fields in message", `[a, b]`, accessLog, false},
		{"exact field count", `[a, b, c]`, "one two three", true},
		{"positional reference", `[a, b = two && $1 = one, c]`, "one two three", true},
		{"positional reference, no match", `[a, b = two && $1 = three, c]`, "one two three", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := match(t, tc.pattern, tc.message); got != tc.want {
				t.Errorf("pattern %s against %s = %v, want %v", tc.pattern, tc.message, got, tc.want)
			}
		})
	}
}

func TestSpaceDelimitedPattern_Invalid(t *testing.T) {
	for _, pattern := range []string{
		`[]`,
		`[a, b`,
		`[a, ..., b, ...]`,
		`[a, b = ]`,
		`[a, b = 1 && undeclared = 2]`,
		`[a, "quoted"]`,
	} {
		if _, err := compileFilterPattern(pattern); err == nil {
			t.Errorf("expected %s to be rejected", pattern)
		}
	}
}

func TestTermPattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		message string
		want    bool
	}{
		{"single term", "ERROR", "[ERROR] boom", true},
		{"single term absent", "ERROR", "[INFO] fine", false},
		{"case sensitive", "error", "[ERROR] boom", false},
		{"terms are ANDed", "ERROR boom", "[ERROR] boom", true},
		{"one term missing", "ERROR splat", "[ERROR] boom", false},
		{"quoted phrase", `"connection refused"`, "dial tcp: connection refused", true},
		{"quoted phrase absent", `"connection refused"`, "dial tcp: connection reset", false},
		{"excluded term", "ERROR -timeout", "[ERROR] boom", true},
		{"excluded term present", "ERROR -boom", "[ERROR] boom", false},
		{"excluded quoted phrase", `ERROR -"connection refused"`, "[ERROR] connection refused", false},
		{"optional terms, first hit", "?ERROR ?WARN", "[WARN] slow", true},
		{"optional terms, none hit", "?ERROR ?WARN", "[INFO] fine", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := match(t, tc.pattern, tc.message); got != tc.want {
				t.Errorf("pattern %q against %q = %v, want %v", tc.pattern, tc.message, got, tc.want)
			}
		})
	}
}

func TestGlobMatch(t *testing.T) {
	tests := []struct {
		pattern, s string
		want       bool
	}{
		{"abc", "abc", true},
		{"abc", "abcd", false},
		{"*", "anything", true},
		{"a*", "abc", true},
		{"a*", "bbc", false},
		{"*c", "abc", true},
		{"a*c", "abc", true},
		{"a*c", "ac", true},
		{"a*a", "a", false},
		{"a*b*c", "axxbyyc", true},
		{"a*b*c", "axxcyyb", false},
		{"", "", true},
		{"", "x", false},
	}
	for _, tc := range tests {
		if got := globMatch(tc.pattern, tc.s); got != tc.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tc.pattern, tc.s, got, tc.want)
		}
	}
}

func TestSplitFields(t *testing.T) {
	got := splitFields(`127.0.0.1 - frank [10/Oct/2000:13:55:36 -0700] "GET /pb.gif HTTP/1.0" 200`)
	want := []field{"127.0.0.1", "-", "frank", "10/Oct/2000:13:55:36 -0700", "GET /pb.gif HTTP/1.0", "200"}
	if len(got) != len(want) {
		t.Fatalf("got %d fields %q, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("field %d = %q, want %q", i, got[i], want[i])
		}
	}
}
