package appsync

import (
	"reflect"
	"testing"
)

func TestParseGraphQL_ShorthandQuery(t *testing.T) {
	typ, field, args, err := parseGraphQL(`{ listNotes { id content } }`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "Query" || field != "listNotes" || len(args) != 0 {
		t.Fatalf("got typ=%q field=%q args=%v", typ, field, args)
	}
}

func TestParseGraphQL_NamedQuery(t *testing.T) {
	typ, field, args, err := parseGraphQL(`query GetNote($id: ID!) { getNote(id: $id) { id content } }`,
		map[string]interface{}{"id": "note-1"})
	if err != nil {
		t.Fatal(err)
	}
	if typ != "Query" || field != "getNote" {
		t.Fatalf("got typ=%q field=%q", typ, field)
	}
	if args["id"] != "note-1" {
		t.Fatalf("expected id=note-1, got %v", args)
	}
}

func TestParseGraphQL_Mutation(t *testing.T) {
	typ, field, args, err := parseGraphQL(
		`mutation { createNote(id: "note-1", content: "Hello") { id content } }`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "Mutation" || field != "createNote" {
		t.Fatalf("got typ=%q field=%q", typ, field)
	}
	if args["id"] != "note-1" || args["content"] != "Hello" {
		t.Fatalf("unexpected args: %v", args)
	}
}

func TestParseGraphQL_MutationNoSelectionSet(t *testing.T) {
	// deleteNote returns a scalar — no sub-selection
	_, field, args, err := parseGraphQL(`mutation { deleteNote(id: "note-1") }`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if field != "deleteNote" || args["id"] != "note-1" {
		t.Fatalf("got field=%q args=%v", field, args)
	}
}

func TestParseGraphQL_NumberArg(t *testing.T) {
	_, _, args, err := parseGraphQL(`query { getPage(page: 2, size: 10) { items } }`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if args["page"] != int64(2) || args["size"] != int64(10) {
		t.Fatalf("unexpected args: %v", args)
	}
}

func TestParseGraphQL_BoolArg(t *testing.T) {
	_, _, args, err := parseGraphQL(`query { search(active: true, deleted: false) { id } }`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if args["active"] != true || args["deleted"] != false {
		t.Fatalf("unexpected args: %v", args)
	}
}

func TestParseGraphQL_NullArg(t *testing.T) {
	_, _, args, err := parseGraphQL(`mutation { update(value: null) { id } }`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := args["value"]; !ok || args["value"] != nil {
		t.Fatalf("unexpected args: %v", args)
	}
}

func TestParseGraphQL_NestedInputObject(t *testing.T) {
	_, _, args, err := parseGraphQL(
		`query { filter(where: {name: "test", active: true}) { id } }`, nil)
	if err != nil {
		t.Fatal(err)
	}
	where, ok := args["where"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected map, got %T", args["where"])
	}
	if where["name"] != "test" || where["active"] != true {
		t.Fatalf("unexpected where: %v", where)
	}
}

func TestParseGraphQL_ListArg(t *testing.T) {
	_, _, args, err := parseGraphQL(`query { byIds(ids: ["a", "b"]) { id } }`, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []interface{}{"a", "b"}
	if !reflect.DeepEqual(args["ids"], want) {
		t.Fatalf("unexpected ids: %v", args["ids"])
	}
}

func TestParseGraphQL_Alias(t *testing.T) {
	// "note: getNote(...)" — alias "note", actual field "getNote"
	_, field, args, err := parseGraphQL(`query { note: getNote(id: "1") { id } }`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if field != "getNote" || args["id"] != "1" {
		t.Fatalf("got field=%q args=%v", field, args)
	}
}

func TestParseGraphQL_VariableSubstitution(t *testing.T) {
	vars := map[string]interface{}{"content": "World", "active": true}
	_, _, args, err := parseGraphQL(
		`mutation Create($content: String!, $active: Boolean!) { createNote(content: $content, active: $active) { id } }`,
		vars)
	if err != nil {
		t.Fatal(err)
	}
	if args["content"] != "World" || args["active"] != true {
		t.Fatalf("unexpected args: %v", args)
	}
}

func TestParseGraphQL_NamedQueryNoOpName(t *testing.T) {
	// "query { ... }" with keyword but no operation name
	typ, field, _, err := parseGraphQL(`query { listNotes { id } }`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "Query" || field != "listNotes" {
		t.Fatalf("got typ=%q field=%q", typ, field)
	}
}

func TestParseGraphQL_Subscription(t *testing.T) {
	typ, field, _, err := parseGraphQL(`subscription { onCreateNote { id } }`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "Subscription" || field != "onCreateNote" {
		t.Fatalf("got typ=%q field=%q", typ, field)
	}
}

func TestParseGraphQL_StringEscapes(t *testing.T) {
	_, _, args, err := parseGraphQL(`mutation { create(title: "hello \"world\"") { id } }`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if args["title"] != `hello "world"` {
		t.Fatalf("unexpected title: %v", args["title"])
	}
}

// --- evalRequestTemplate ---

func TestEvalRequestTemplate_Empty(t *testing.T) {
	b, err := evalRequestTemplate("", "Mutation", "createNote", map[string]interface{}{"id": "1"})
	if err != nil {
		t.Fatal(err)
	}
	// Empty template → default JSON payload
	if string(b) == "" {
		t.Fatal("expected non-empty payload")
	}
}

func TestEvalRequestTemplate_ContextArguments(t *testing.T) {
	tmpl := `{"version":"2017-02-28","operation":"Invoke","payload":{"field":"createNote","args":$util.toJson($context.arguments)}}`
	b, err := evalRequestTemplate(tmpl, "Mutation", "createNote", map[string]interface{}{"id": "note-1"})
	if err != nil {
		t.Fatal(err)
	}
	// Should be valid JSON with args substituted
	got := string(b)
	if got == tmpl {
		t.Fatal("template was not evaluated")
	}
	if !containsStr(got, `"note-1"`) {
		t.Fatalf("expected args in output: %s", got)
	}
}

func TestEvalRequestTemplate_FieldName(t *testing.T) {
	tmpl := `{"field":$context.info.fieldName}`
	b, err := evalRequestTemplate(tmpl, "Query", "getNote", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !containsStr(string(b), `"getNote"`) {
		t.Fatalf("expected fieldName in output: %s", string(b))
	}
}

// --- evalResponseTemplate ---

func TestEvalResponseTemplate_ToJsonResult(t *testing.T) {
	result := []byte(`{"id":"1","content":"hi"}`)
	out, err := evalResponseTemplate("$util.toJson($context.result)", result)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(result) {
		t.Fatalf("want %s got %s", result, out)
	}
}

func TestEvalResponseTemplate_ContextResult(t *testing.T) {
	result := []byte(`true`)
	out, err := evalResponseTemplate("$context.result", result)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "true" {
		t.Fatalf("want true got %s", out)
	}
}

func TestEvalResponseTemplate_Empty(t *testing.T) {
	result := []byte(`"ok"`)
	out, err := evalResponseTemplate("", result)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `"ok"` {
		t.Fatalf("want \"ok\" got %s", out)
	}
}

func TestEvalResponseTemplate_NilResult(t *testing.T) {
	out, err := evalResponseTemplate("$util.toJson($context.result)", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "null" {
		t.Fatalf("want null got %s", out)
	}
}

func TestEvalResponseTemplate_HardcodedJSON(t *testing.T) {
	out, err := evalResponseTemplate(`"pong"`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `"pong"` {
		t.Fatalf("want \"pong\" got %s", out)
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
