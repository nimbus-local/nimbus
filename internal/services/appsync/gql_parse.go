package appsync

import (
	"fmt"
	"strconv"
)

// parseGraphQL extracts the operation type name ("Query"/"Mutation"/"Subscription"),
// the first field name, and its argument map from a GraphQL query string.
// variables is merged in when the query uses $var syntax.
func parseGraphQL(query string, variables map[string]interface{}) (typeName, fieldName string, args map[string]interface{}, err error) {
	p := &gqlParser{src: query, variables: variables}
	return p.parse()
}

type gqlParser struct {
	src       string
	pos       int
	variables map[string]interface{}
}

func (p *gqlParser) skipWS() {
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ',':
			p.pos++
		case c == '#':
			for p.pos < len(p.src) && p.src[p.pos] != '\n' {
				p.pos++
			}
		default:
			return
		}
	}
}

func (p *gqlParser) readName() string {
	p.skipWS()
	start := p.pos
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			p.pos++
		} else {
			break
		}
	}
	return p.src[start:p.pos]
}

func (p *gqlParser) consume(c byte) error {
	p.skipWS()
	if p.pos >= len(p.src) {
		return fmt.Errorf("expected '%c', got EOF", c)
	}
	if p.src[p.pos] != c {
		return fmt.Errorf("expected '%c', got '%c'", c, p.src[p.pos])
	}
	p.pos++
	return nil
}

// skipParens skips balanced parentheses; caller has already consumed the opening '('.
func (p *gqlParser) skipParens() {
	depth := 1
	for p.pos < len(p.src) && depth > 0 {
		c := p.src[p.pos]
		if c == '(' {
			depth++
		} else if c == ')' {
			depth--
		}
		p.pos++
	}
}

func (p *gqlParser) parse() (typeName, fieldName string, args map[string]interface{}, err error) {
	p.skipWS()

	typeName = "Query"
	if p.pos < len(p.src) && p.src[p.pos] != '{' {
		kw := p.readName()
		switch kw {
		case "mutation":
			typeName = "Mutation"
		case "subscription":
			typeName = "Subscription"
		}

		// Skip optional operation name
		p.skipWS()
		if p.pos < len(p.src) && p.src[p.pos] != '{' && p.src[p.pos] != '(' {
			p.readName()
		}

		// Skip variable definitions: ($x: Type = default, ...)
		p.skipWS()
		if p.pos < len(p.src) && p.src[p.pos] == '(' {
			p.pos++
			p.skipParens()
		}

		// Skip directives: @name(...)
		p.skipWS()
		for p.pos < len(p.src) && p.src[p.pos] == '@' {
			p.pos++ // '@'
			p.readName()
			p.skipWS()
			if p.pos < len(p.src) && p.src[p.pos] == '(' {
				p.pos++
				p.skipParens()
			}
			p.skipWS()
		}
	}

	if err = p.consume('{'); err != nil {
		return
	}

	// First field (possibly aliased: alias: actualName)
	fieldName = p.readName()
	if fieldName == "" {
		err = fmt.Errorf("empty selection set")
		return
	}

	p.skipWS()
	if p.pos < len(p.src) && p.src[p.pos] == ':' {
		p.pos++ // consume ':'
		fieldName = p.readName()
	}

	// Arguments
	args = map[string]interface{}{}
	p.skipWS()
	if p.pos < len(p.src) && p.src[p.pos] == '(' {
		p.pos++ // consume '('
		args, err = p.parseArgList()
		if err != nil {
			return
		}
		err = p.consume(')')
	}

	return
}

func (p *gqlParser) parseArgList() (map[string]interface{}, error) {
	args := map[string]interface{}{}
	for {
		p.skipWS()
		if p.pos >= len(p.src) || p.src[p.pos] == ')' {
			break
		}
		key := p.readName()
		if key == "" {
			break
		}
		if err := p.consume(':'); err != nil {
			return nil, err
		}
		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		args[key] = val
	}
	return args, nil
}

func (p *gqlParser) parseValue() (interface{}, error) {
	p.skipWS()
	if p.pos >= len(p.src) {
		return nil, fmt.Errorf("unexpected EOF in value")
	}
	c := p.src[p.pos]
	switch {
	case c == '"':
		return p.parseString()
	case c == '$':
		return p.parseVariable()
	case c == '[':
		return p.parseList()
	case c == '{':
		return p.parseInputObject()
	case c == '-' || (c >= '0' && c <= '9'):
		return p.parseNumber()
	default:
		name := p.readName()
		switch name {
		case "true":
			return true, nil
		case "false":
			return false, nil
		case "null":
			return nil, nil
		case "":
			return nil, fmt.Errorf("unexpected character '%c'", c)
		default:
			return name, nil // enum value
		}
	}
}

func (p *gqlParser) parseString() (string, error) {
	p.pos++ // opening '"'
	var buf []byte
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == '"' {
			p.pos++
			return string(buf), nil
		}
		if c == '\\' {
			p.pos++
			if p.pos >= len(p.src) {
				break
			}
			switch p.src[p.pos] {
			case '"':
				buf = append(buf, '"')
			case '\\':
				buf = append(buf, '\\')
			case '/':
				buf = append(buf, '/')
			case 'n':
				buf = append(buf, '\n')
			case 'r':
				buf = append(buf, '\r')
			case 't':
				buf = append(buf, '\t')
			case 'b':
				buf = append(buf, '\b')
			case 'f':
				buf = append(buf, '\f')
			default:
				buf = append(buf, '\\', p.src[p.pos])
			}
		} else {
			buf = append(buf, c)
		}
		p.pos++
	}
	return "", fmt.Errorf("unterminated string")
}

func (p *gqlParser) parseNumber() (interface{}, error) {
	start := p.pos
	if p.src[p.pos] == '-' {
		p.pos++
	}
	for p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
		p.pos++
	}
	isFloat := false
	if p.pos < len(p.src) && p.src[p.pos] == '.' {
		isFloat = true
		p.pos++
		for p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
			p.pos++
		}
	}
	if p.pos < len(p.src) && (p.src[p.pos] == 'e' || p.src[p.pos] == 'E') {
		isFloat = true
		p.pos++
		if p.pos < len(p.src) && (p.src[p.pos] == '+' || p.src[p.pos] == '-') {
			p.pos++
		}
		for p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
			p.pos++
		}
	}
	numStr := p.src[start:p.pos]
	if isFloat {
		return strconv.ParseFloat(numStr, 64)
	}
	n, err := strconv.ParseInt(numStr, 10, 64)
	return n, err
}

func (p *gqlParser) parseVariable() (interface{}, error) {
	p.pos++ // '$'
	name := p.readName()
	if p.variables != nil {
		if val, ok := p.variables[name]; ok {
			return val, nil
		}
	}
	return nil, nil
}

func (p *gqlParser) parseList() (interface{}, error) {
	p.pos++ // '['
	var items []interface{}
	for {
		p.skipWS()
		if p.pos >= len(p.src) || p.src[p.pos] == ']' {
			break
		}
		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		items = append(items, val)
	}
	if err := p.consume(']'); err != nil {
		return nil, err
	}
	if items == nil {
		items = []interface{}{}
	}
	return items, nil
}

func (p *gqlParser) parseInputObject() (interface{}, error) {
	p.pos++ // '{'
	obj := map[string]interface{}{}
	for {
		p.skipWS()
		if p.pos >= len(p.src) || p.src[p.pos] == '}' {
			break
		}
		key := p.readName()
		if key == "" {
			break
		}
		if err := p.consume(':'); err != nil {
			return nil, err
		}
		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		obj[key] = val
	}
	if err := p.consume('}'); err != nil {
		return nil, err
	}
	return obj, nil
}
