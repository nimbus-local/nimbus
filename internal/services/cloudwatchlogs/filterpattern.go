package cloudwatchlogs

// CloudWatch Logs filter-pattern matching.
//
// Three pattern forms are recognised, following the AWS syntax:
//
//	JSON             { $.Type = "Task" && $.Version >= 2 }
//	space-delimited  [ip, user, ..., status_code = 4*]
//	terms            ERROR -INFO "connection refused"
//
// A pattern that opens with '{' or '[' but does not parse is rejected with an
// error, which the caller reports as InvalidParameterException. Returning zero
// events instead is how an unsupported pattern goes unnoticed.

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// matcher reports whether a log event message satisfies a filter pattern.
type matcher func(message string) bool

// compileFilterPattern builds a matcher for pattern. An empty pattern compiles
// to a nil matcher, which callers read as "keep every event".
func compileFilterPattern(pattern string) (matcher, error) {
	p := strings.TrimSpace(pattern)
	switch {
	case p == "":
		return nil, nil
	case strings.HasPrefix(p, "{"):
		return compileJSONPattern(p)
	case strings.HasPrefix(p, "["):
		return compileSpacePattern(p)
	default:
		return compileTermPattern(p), nil
	}
}

// --- JSON patterns: { $.field = "value" && $.other >= 3 } ---

func compileJSONPattern(p string) (matcher, error) {
	end := strings.LastIndex(p, "}")
	if end < 0 {
		return nil, fmt.Errorf("missing closing }")
	}
	expr, err := parseExpr(p[1:end], jsonRef)
	if err != nil {
		return nil, err
	}
	return func(message string) bool {
		root, ok := decodeJSON(message)
		if !ok {
			return false // a JSON pattern only ever matches a JSON event
		}
		return expr.eval(func(ref reference) (interface{}, bool) {
			return resolvePath(root, ref.path)
		})
	}, nil
}

// decodeJSON parses a log message as JSON. Numbers are kept as json.Number so
// that comparisons see the value the event carried, not a float32 round-trip.
func decodeJSON(message string) (interface{}, bool) {
	trimmed := strings.TrimSpace(message)
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return nil, false
	}
	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.UseNumber()
	var root interface{}
	if err := dec.Decode(&root); err != nil {
		return nil, false
	}
	return root, true
}

// jsonRef resolves the left-hand side of a condition in a JSON pattern, which
// is always a $ selector.
func jsonRef(t token) (reference, error) {
	if t.kind != tkSelector {
		return reference{}, fmt.Errorf("expected a $ selector, got %q", t.text)
	}
	path, err := parseSelector(t.text)
	if err != nil {
		return reference{}, err
	}
	return reference{path: path}, nil
}

// pathStep is one hop of a $ selector: a member name or an array index.
type pathStep struct {
	name  string
	index int
	isIdx bool
}

// parseSelector turns `$.Containers[0].Name` into its path steps.
func parseSelector(s string) ([]pathStep, error) {
	if !strings.HasPrefix(s, "$") {
		return nil, fmt.Errorf("selector %q must start with $", s)
	}
	var path []pathStep
	i := 1
	for i < len(s) {
		switch s[i] {
		case '.':
			i++
			start := i
			for i < len(s) && isNameByte(s[i]) {
				i++
			}
			if i == start {
				return nil, fmt.Errorf("selector %q has an empty member name", s)
			}
			path = append(path, pathStep{name: s[start:i]})
		case '[':
			end := strings.IndexByte(s[i:], ']')
			if end < 0 {
				return nil, fmt.Errorf("selector %q has an unterminated [", s)
			}
			inner := strings.TrimSpace(s[i+1 : i+end])
			i += end + 1
			if name, quoted := stripQuotes(inner); quoted {
				path = append(path, pathStep{name: name})
				continue
			}
			idx, err := strconv.Atoi(inner)
			if err != nil {
				return nil, fmt.Errorf("selector %q has a non-numeric index %q", s, inner)
			}
			path = append(path, pathStep{index: idx, isIdx: true})
		default:
			return nil, fmt.Errorf("unexpected %q in selector %q", string(s[i]), s)
		}
	}
	return path, nil
}

// resolvePath walks a decoded JSON document. The second result is false when
// the path names something the document does not have — the case NOT EXISTS
// tests for, and the case every other operator treats as "no match".
func resolvePath(root interface{}, path []pathStep) (interface{}, bool) {
	cur := root
	for _, st := range path {
		if st.isIdx {
			arr, ok := cur.([]interface{})
			if !ok || st.index < 0 || st.index >= len(arr) {
				return nil, false
			}
			cur = arr[st.index]
			continue
		}
		obj, ok := cur.(map[string]interface{})
		if !ok {
			return nil, false
		}
		v, ok := obj[st.name]
		if !ok {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

// --- Space-delimited patterns: [ip, user, ..., status_code = 404] ---

func compileSpacePattern(p string) (matcher, error) {
	end := strings.LastIndex(p, "]")
	if end < 0 {
		return nil, fmt.Errorf("missing closing ]")
	}
	toks, err := lex(p[1:end])
	if err != nil {
		return nil, err
	}
	elements := splitOnCommas(toks)

	// Fields declared before the ellipsis are counted from the start of the
	// message and fields after it from the end, so the ellipsis can absorb any
	// number of fields in between.
	ellipsis := -1
	for i, el := range elements {
		if isEllipsis(el) {
			if ellipsis >= 0 {
				return nil, fmt.Errorf("only one ... is allowed")
			}
			ellipsis = i
		}
	}
	names := map[string]reference{}
	refOf := func(i int) reference {
		if ellipsis < 0 || i < ellipsis {
			return reference{index: i}
		}
		return reference{index: -(len(elements) - i)}
	}
	for i, el := range elements {
		if isEllipsis(el) {
			continue
		}
		if el[0].kind != tkWord {
			return nil, fmt.Errorf("field %d must start with a name, got %q", i+1, el[0].text)
		}
		names[el[0].text] = refOf(i)
	}

	resolve := func(t token) (reference, error) {
		switch t.kind {
		case tkWord:
			ref, ok := names[t.text]
			if !ok {
				return reference{}, fmt.Errorf("condition names undeclared field %q", t.text)
			}
			return ref, nil
		case tkSelector:
			n, err := strconv.Atoi(strings.TrimPrefix(t.text, "$"))
			if err != nil || n < 1 {
				return reference{}, fmt.Errorf("%q is not a field position like $1", t.text)
			}
			return reference{index: n - 1}, nil
		}
		return reference{}, fmt.Errorf("expected a field name, got %q", t.text)
	}

	var conds []node
	for _, el := range elements {
		if isEllipsis(el) || len(el) <= 2 { // name + EOF: a placeholder, matches anything
			continue
		}
		cond, err := parseExprTokens(el, resolve)
		if err != nil {
			return nil, err
		}
		conds = append(conds, cond)
	}

	fieldCount := len(elements)
	if ellipsis >= 0 {
		fieldCount-- // the ellipsis itself declares no field
	}
	return func(message string) bool {
		fields := splitFields(message)
		if ellipsis < 0 && len(fields) != fieldCount {
			return false
		}
		if ellipsis >= 0 && len(fields) < fieldCount {
			return false
		}
		lookup := func(ref reference) (interface{}, bool) {
			i := ref.index
			if i < 0 {
				i += len(fields)
			}
			if i < 0 || i >= len(fields) {
				return nil, false
			}
			return fields[i], true
		}
		for _, c := range conds {
			if !c.eval(lookup) {
				return false
			}
		}
		return true
	}, nil
}

func isEllipsis(el []token) bool {
	return len(el) > 0 && el[0].kind == tkWord && el[0].text == "..."
}

// field is one token of a space-delimited message. It compares as a string and,
// where it parses as one, as a number — the pattern decides which.
type field string

// splitFields splits a message into the fields a space-delimited pattern
// matches against: whitespace-separated, with bracketed and double-quoted runs
// kept whole, so an Apache-style `[10/Oct/2000:13:55:36 -0700]` is one field.
func splitFields(message string) []field {
	var fields []field
	i := 0
	for i < len(message) {
		if isSpaceByte(message[i]) {
			i++
			continue
		}
		var closing byte
		switch message[i] {
		case '[':
			closing = ']'
		case '"':
			closing = '"'
		}
		if closing != 0 {
			if end := strings.IndexByte(message[i+1:], closing); end >= 0 {
				fields = append(fields, field(message[i+1:i+1+end]))
				i += end + 2
				continue
			}
		}
		start := i
		for i < len(message) && !isSpaceByte(message[i]) {
			i++
		}
		fields = append(fields, field(message[start:i]))
	}
	return fields
}

// --- Term patterns: ERROR -INFO "connection refused" ---

// compileTermPattern matches unstructured messages. Bare terms must all be
// present, `-` terms must be absent, and when any `?` term is given at least
// one of them must be present.
func compileTermPattern(p string) matcher {
	var required, optional, excluded []string
	for _, t := range splitTerms(p) {
		switch {
		case len(t) > 1 && t[0] == '-':
			excluded = append(excluded, t[1:])
		case len(t) > 1 && t[0] == '?':
			optional = append(optional, t[1:])
		default:
			required = append(required, t)
		}
	}
	return func(message string) bool {
		for _, t := range excluded {
			if strings.Contains(message, t) {
				return false
			}
		}
		for _, t := range required {
			if !strings.Contains(message, t) {
				return false
			}
		}
		for _, t := range optional {
			if strings.Contains(message, t) {
				return true
			}
		}
		return len(optional) == 0
	}
}

// splitTerms splits a term pattern on whitespace, keeping quoted runs whole and
// dropping the quotes. A leading - or ? stays attached to its term.
func splitTerms(p string) []string {
	var terms []string
	var cur strings.Builder
	quoted := false
	flush := func() {
		if cur.Len() > 0 {
			terms = append(terms, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(p); i++ {
		switch c := p[i]; {
		case c == '"':
			quoted = !quoted
		case !quoted && isSpaceByte(c):
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return terms
}

// --- Conditions ---

// A reference is the left-hand side of a condition: a JSON selector path, or a
// field position in a space-delimited message (negative counts from the end).
type reference struct {
	path  []pathStep
	index int
}

type lookupFunc func(reference) (interface{}, bool)

type node interface {
	eval(lookup lookupFunc) bool
}

type binaryNode struct {
	and  bool
	l, r node
}

func (n binaryNode) eval(lookup lookupFunc) bool {
	if n.and {
		return n.l.eval(lookup) && n.r.eval(lookup)
	}
	return n.l.eval(lookup) || n.r.eval(lookup)
}

type condNode struct {
	ref  reference
	cond condition
}

func (n condNode) eval(lookup lookupFunc) bool {
	v, exists := lookup(n.ref)
	return n.cond.match(v, exists)
}

// Keyword operators, spelled as they appear in a pattern.
const (
	opIsTrue    = "IS TRUE"
	opIsFalse   = "IS FALSE"
	opIsNull    = "IS NULL"
	opNotExists = "NOT EXISTS"
)

type condition struct {
	op  string
	lit literal
}

func (c condition) match(v interface{}, exists bool) bool {
	switch c.op {
	case opNotExists:
		return !exists
	case opIsNull:
		return exists && v == nil
	case opIsTrue, opIsFalse:
		b, ok := v.(bool)
		return exists && ok && b == (c.op == opIsTrue)
	}
	if !exists {
		return false // a value that isn't there matches no comparison
	}
	return compareValue(v, c.op, c.lit)
}

type litKind int

const (
	litString litKind = iota
	litNumber
	litBool
	litNull
)

type literal struct {
	kind litKind
	s    string // string literals may contain '*' wildcards
	n    float64
	b    bool
}

// compareValue applies one operator to a value from the event. A value of the
// wrong type never satisfies a comparison other than !=, which it satisfies by
// definition: it is not the literal.
func compareValue(v interface{}, op string, lit literal) bool {
	switch lit.kind {
	case litNull:
		switch op {
		case "=":
			return v == nil
		case "!=":
			return v != nil
		}
		return false
	case litBool:
		b, ok := v.(bool)
		switch op {
		case "=":
			return ok && b == lit.b
		case "!=":
			return !ok || b != lit.b
		}
		return false
	case litNumber:
		n, ok := toNumber(v)
		if !ok {
			return op == "!="
		}
		switch op {
		case "=":
			return n == lit.n
		case "!=":
			return n != lit.n
		case "<":
			return n < lit.n
		case "<=":
			return n <= lit.n
		case ">":
			return n > lit.n
		case ">=":
			return n >= lit.n
		}
		return false
	default:
		s, ok := toString(v)
		if !ok {
			return op == "!="
		}
		switch op {
		case "=":
			return globMatch(lit.s, s)
		case "!=":
			return !globMatch(lit.s, s)
		case "<":
			return s < lit.s
		case "<=":
			return s <= lit.s
		case ">":
			return s > lit.s
		case ">=":
			return s >= lit.s
		}
		return false
	}
}

func toNumber(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	case field:
		f, err := strconv.ParseFloat(string(n), 64)
		return f, err == nil
	}
	return 0, false
}

func toString(v interface{}) (string, bool) {
	switch s := v.(type) {
	case string:
		return s, true
	case field:
		return string(s), true
	}
	return "", false
}

// globMatch reports whether s matches pattern, in which '*' stands for any run
// of characters. Filter patterns have no other metacharacter.
func globMatch(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	last := parts[len(parts)-1]
	for _, part := range parts[1 : len(parts)-1] {
		i := strings.Index(s, part)
		if i < 0 {
			return false
		}
		s = s[i+len(part):]
	}
	return strings.HasSuffix(s, last)
}

// --- Lexer ---

type tokKind int

const (
	tkEOF tokKind = iota
	tkSelector
	tkWord   // bare word: a keyword, a field name, or an unquoted literal
	tkString // quoted literal, quotes stripped
	tkOp     // = != < <= > >=
	tkAnd
	tkOr
	tkLParen
	tkRParen
	tkComma
)

type token struct {
	kind tokKind
	text string
}

func lex(s string) ([]token, error) {
	var toks []token
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case isSpaceByte(c):
			i++
		case c == '(':
			toks = append(toks, token{tkLParen, "("})
			i++
		case c == ')':
			toks = append(toks, token{tkRParen, ")"})
			i++
		case c == ',':
			toks = append(toks, token{tkComma, ","})
			i++
		case c == '&' || c == '|':
			if i+1 >= len(s) || s[i+1] != c {
				return nil, fmt.Errorf("unexpected %q, did you mean %q?", string(c), strings.Repeat(string(c), 2))
			}
			kind := tkAnd
			if c == '|' {
				kind = tkOr
			}
			toks = append(toks, token{kind, s[i : i+2]})
			i += 2
		case c == '=':
			i++
			if i < len(s) && s[i] == '=' { // == is accepted for =
				i++
			}
			toks = append(toks, token{tkOp, "="})
		case c == '!' || c == '<' || c == '>':
			op := string(c)
			i++
			switch {
			case i < len(s) && s[i] == '=':
				op += "="
				i++
			case c == '<' && i < len(s) && s[i] == '>':
				op = "!="
				i++
			case c == '!':
				return nil, fmt.Errorf("unexpected %q, did you mean %q?", "!", "!=")
			}
			toks = append(toks, token{tkOp, op})
		case c == '"' || c == '\'':
			text, n, err := lexQuoted(s[i:])
			if err != nil {
				return nil, err
			}
			toks = append(toks, token{tkString, text})
			i += n
		case c == '$':
			j := scanSelector(s[i:])
			toks = append(toks, token{tkSelector, s[i : i+j]})
			i += j
		default:
			start := i
			for i < len(s) && isWordByte(s[i]) {
				i++
			}
			toks = append(toks, token{tkWord, s[start:i]})
		}
	}
	return append(toks, token{kind: tkEOF}), nil
}

// scanSelector returns the length of the selector starting at s[0] == '$'.
func scanSelector(s string) int {
	i := 1
	for i < len(s) {
		switch {
		case s[i] == '.':
			i++
			for i < len(s) && isNameByte(s[i]) {
				i++
			}
		case s[i] == '[':
			end := strings.IndexByte(s[i:], ']')
			if end < 0 {
				return len(s) // unterminated; parseSelector reports it
			}
			i += end + 1
		case isNameByte(s[i]):
			i++
		default:
			return i
		}
	}
	return i
}

func lexQuoted(s string) (string, int, error) {
	quote := s[0]
	var b strings.Builder
	for i := 1; i < len(s); {
		switch {
		case s[i] == '\\' && i+1 < len(s):
			b.WriteByte(s[i+1])
			i += 2
		case s[i] == quote:
			return b.String(), i + 1, nil
		default:
			b.WriteByte(s[i])
			i++
		}
	}
	return "", 0, fmt.Errorf("unterminated string literal")
}

func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

func isNameByte(c byte) bool {
	return c == '_' || c == '-' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// isWordByte reports whether c can appear in a bare word. Words are permissive
// so that unquoted values like 4* or /api/health lex as one token.
func isWordByte(c byte) bool {
	switch c {
	case '(', ')', ',', '=', '<', '>', '!', '&', '|', '"', '\'', '$', '[', ']':
		return false
	}
	return !isSpaceByte(c)
}

// --- Parser ---

// refResolver turns the left-hand token of a condition into a reference.
type refResolver func(token) (reference, error)

func parseExpr(src string, ref refResolver) (node, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	return parseExprTokens(toks, ref)
}

func parseExprTokens(toks []token, ref refResolver) (node, error) {
	p := &parser{toks: toks, ref: ref}
	n, err := p.expr()
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t.kind != tkEOF {
		return nil, fmt.Errorf("unexpected %q", t.text)
	}
	return n, nil
}

type parser struct {
	toks []token
	pos  int
	ref  refResolver
}

func (p *parser) peek() token { return p.toks[p.pos] }

func (p *parser) next() token {
	t := p.toks[p.pos]
	if t.kind != tkEOF {
		p.pos++
	}
	return t
}

func (p *parser) expr() (node, error) {
	left, err := p.and()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tkOr {
		p.next()
		right, err := p.and()
		if err != nil {
			return nil, err
		}
		left = binaryNode{l: left, r: right}
	}
	return left, nil
}

func (p *parser) and() (node, error) {
	left, err := p.operand()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tkAnd {
		p.next()
		right, err := p.operand()
		if err != nil {
			return nil, err
		}
		left = binaryNode{and: true, l: left, r: right}
	}
	return left, nil
}

func (p *parser) operand() (node, error) {
	switch p.peek().kind {
	case tkEOF:
		return nil, fmt.Errorf("unexpected end of pattern")
	case tkLParen:
		p.next()
		n, err := p.expr()
		if err != nil {
			return nil, err
		}
		if p.next().kind != tkRParen {
			return nil, fmt.Errorf("missing closing )")
		}
		return n, nil
	}
	ref, err := p.ref(p.next())
	if err != nil {
		return nil, err
	}
	cond, err := p.condition()
	if err != nil {
		return nil, err
	}
	return condNode{ref: ref, cond: cond}, nil
}

func (p *parser) condition() (condition, error) {
	t := p.peek()
	switch {
	case t.kind == tkOp:
		p.next()
		lit, err := literalFromToken(p.next())
		if err != nil {
			return condition{}, err
		}
		return condition{op: t.text, lit: lit}, nil
	case t.kind == tkWord && strings.EqualFold(t.text, "IS"):
		p.next()
		kw := p.next()
		switch strings.ToUpper(kw.text) {
		case "TRUE":
			return condition{op: opIsTrue}, nil
		case "FALSE":
			return condition{op: opIsFalse}, nil
		case "NULL":
			return condition{op: opIsNull}, nil
		}
		return condition{}, fmt.Errorf("IS must be followed by TRUE, FALSE or NULL, got %q", kw.text)
	case t.kind == tkWord && strings.EqualFold(t.text, "NOT"):
		p.next()
		if kw := p.next(); !strings.EqualFold(kw.text, "EXISTS") {
			return condition{}, fmt.Errorf("NOT must be followed by EXISTS, got %q", kw.text)
		}
		return condition{op: opNotExists}, nil
	}
	return condition{}, fmt.Errorf("expected a comparison, got %q", t.text)
}

func literalFromToken(t token) (literal, error) {
	switch t.kind {
	case tkString:
		return literal{kind: litString, s: t.text}, nil
	case tkWord:
		switch strings.ToUpper(t.text) {
		case "TRUE":
			return literal{kind: litBool, b: true}, nil
		case "FALSE":
			return literal{kind: litBool}, nil
		case "NULL":
			return literal{kind: litNull}, nil
		}
		if n, err := strconv.ParseFloat(t.text, 64); err == nil {
			return literal{kind: litNumber, n: n}, nil
		}
		return literal{kind: litString, s: t.text}, nil
	}
	return literal{}, fmt.Errorf("expected a value, got %q", t.text)
}

// --- Token helpers ---

// splitOnCommas breaks a token stream into the comma-separated elements of a
// space-delimited pattern. Each element ends with an EOF token so the parser
// can consume it on its own.
func splitOnCommas(toks []token) [][]token {
	var out [][]token
	var cur []token
	depth := 0
	for _, t := range toks {
		switch t.kind {
		case tkLParen:
			depth++
		case tkRParen:
			depth--
		case tkComma:
			if depth == 0 {
				out = append(out, append(cur, token{kind: tkEOF}))
				cur = nil
				continue
			}
		case tkEOF:
			continue
		}
		cur = append(cur, t)
	}
	return append(out, append(cur, token{kind: tkEOF}))
}

func stripQuotes(s string) (string, bool) {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1], true
	}
	return "", false
}
