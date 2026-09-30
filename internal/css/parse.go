package css

import (
	"strings"
)

type Diagnostic struct {
	Line, Col int
	Message   string
}
type Declaration struct {
	Property, Value string
	Important       bool
	Line, Col       int
}
type Rule struct {
	Selectors    []Selector
	Declarations []Declaration
}
type Sheet struct {
	Rules       []Rule
	Diagnostics []Diagnostic
}

func Parse(src string) Sheet {
	ts := Tokenize(src)
	sheet := Sheet{}
	diag := func(t Token, msg string) {
		sheet.Diagnostics = append(sheet.Diagnostics, Diagnostic{t.Line, t.Col, msg})
	}
	for i := 0; i < len(ts); {
		for i < len(ts) && ts[i].Kind == "space" {
			i++
		}
		if i >= len(ts) {
			break
		}
		start := i
		for i < len(ts) && ts[i].Kind != "{" && ts[i].Kind != "}" && ts[i].Kind != ";" {
			i++
		}
		if i >= len(ts) {
			diag(ts[start], "malformed rule")
			break
		}
		if ts[i].Kind != "{" {
			diag(ts[start], "malformed rule")
			i++
			continue
		}
		sels, ok := parseSelectors(ts[start:i])
		i++
		depth := 1
		body := i
		for i < len(ts) && depth > 0 {
			switch ts[i].Kind {
			case "{":
				depth++
			case "}":
				depth--
			}
			i++
		}
		if depth > 0 {
			diag(ts[start], "unclosed rule")
			break
		}
		if !ok {
			diag(ts[start], "invalid selector")
			continue
		}
		r := Rule{Selectors: sels}
		contents := ts[body : i-1]
		for j := 0; j < len(contents); {
			for j < len(contents) && (contents[j].Kind == "space" || contents[j].Kind == ";") {
				j++
			}
			if j >= len(contents) {
				break
			}
			b := j
			// Consume component values: delimiters inside functions or blocks are not
			// declaration terminators. A stray block is an invalid declaration.
			depthParen, depthBracket, depthBrace := 0, 0, 0
			malformedBlock := false
			for j < len(contents) {
				switch contents[j].Kind {
				case "function", "(":
					depthParen++
				case ")":
					if depthParen > 0 {
						depthParen--
					}
				case "[":
					depthBracket++
				case "]":
					if depthBracket > 0 {
						depthBracket--
					}
				case "{":
					depthBrace++
					malformedBlock = true
				case "}":
					if depthBrace > 0 {
						depthBrace--
					}
				}
				if contents[j].Kind == ";" && depthParen == 0 && depthBracket == 0 && depthBrace == 0 {
					break
				}
				j++
			}
			end := j
			if malformedBlock {
				diag(contents[b], "malformed declaration")
				if j < len(contents) {
					j++
				}
				continue
			}
			if j < len(contents) {
				j++
			}
			colon := -1
			for k := b; k < end; k++ {
				if contents[k].Kind == ":" {
					colon = k
					break
				}
			}
			if colon < 0 {
				diag(contents[b], "malformed declaration")
				continue
			}
			name := strings.TrimSpace(raw(contents[b:colon]))
			value := strings.TrimSpace(raw(contents[colon+1 : end]))
			if name == "" || value == "" && !strings.HasPrefix(name, "--") {
				diag(contents[b], "malformed declaration")
				continue
			}
			important := false
			v := Tokenize(value)
			e := len(v) - 1
			for e >= 0 && v[e].Kind == "space" {
				e--
			}
			if e >= 0 && strings.EqualFold(v[e].Value, "important") {
				k := e - 1
				for k >= 0 && v[k].Kind == "space" {
					k--
				}
				if k >= 0 && v[k].Kind == "!" {
					important = true
					value = strings.TrimSpace(raw(v[:k]))
				}
			}
			if !strings.HasPrefix(name, "--") {
				name = strings.ToLower(name)
			}
			d := Declaration{name, value, important, contents[b].Line, contents[b].Col}
			if len(compileDecls([]Declaration{d})) == 0 {
				msg := "invalid value for " + name
				if !strings.HasPrefix(name, "--") && len(shorthandTargets(name)) == 0 {
					if _, known := propertyIndex(name); !known {
						msg = "unknown property: " + name
					}
				}
				diag(contents[b], msg)
			}
			r.Declarations = append(r.Declarations, d)
		}
		sheet.Rules = append(sheet.Rules, r)
	}
	return sheet
}
func raw(ts []Token) string {
	var b strings.Builder
	for _, t := range ts {
		b.WriteString(t.Raw)
	}
	return b.String()
}
