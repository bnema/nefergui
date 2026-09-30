package css

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Token is a CSS syntax token. Raw preserves the original spelling for value parsing.
type Token struct {
	Kind, Raw, Value string
	Line, Col        int
}

// Tokenize scans CSS without panicking on malformed UTF-8 or unterminated constructs.
func Tokenize(src string) []Token {
	var out []Token
	line, col := 1, 1
	take := func(n int) string {
		s := src[:n]
		for _, r := range s {
			if r == '\n' {
				line++
				col = 1
			} else {
				col++
			}
		}
		src = src[n:]
		return s
	}
	emit := func(k, s, v string, l, c int) { out = append(out, Token{k, s, v, l, c}) }
	for len(src) > 0 {
		l, c := line, col
		if strings.HasPrefix(src, "/*") {
			i := strings.Index(src[2:], "*/")
			if i < 0 {
				take(len(src))
			} else {
				take(i + 4)
			}
			// Comments separate tokens; raw() must preserve that separation.
			emit("space", " ", " ", l, c)
			continue
		}
		r, n := utf8.DecodeRuneInString(src)
		if unicode.IsSpace(r) {
			i := n
			for i < len(src) {
				rr, nn := utf8.DecodeRuneInString(src[i:])
				if !unicode.IsSpace(rr) {
					break
				}
				i += nn
			}
			emit("space", take(i), " ", l, c)
			continue
		}
		if r == '\'' || r == '"' {
			quote := r
			i := n
			var val strings.Builder
			bad := false
			closed := false
			for i < len(src) {
				rr, nn := utf8.DecodeRuneInString(src[i:])
				if rr == quote {
					i += nn
					closed = true
					break
				}
				if rr == '\n' {
					bad = true
					break
				}
				if rr == '\\' {
					i += nn
					if i < len(src) {
						er, en := escape(src[i:])
						val.WriteRune(er)
						i += en
					}
					continue
				}
				val.WriteRune(rr)
				i += nn
			}
			if !closed {
				bad = true
			}
			s := take(i)
			k := "string"
			if bad {
				k = "bad-string"
			}
			emit(k, s, val.String(), l, c)
			continue
		}
		if r == '#' && len(src) > n && identStart(src[n:]) {
			i := n + identLen(src[n:])
			s := take(i)
			emit("hash", s, unescapeIdent(s[n:]), l, c)
			continue
		}
		if numberStart(src) {
			i := numberLen(src)
			k := "number"
			if i < len(src) && src[i] == '%' {
				i++
				k = "percentage"
			} else if i < len(src) && identStart(src[i:]) {
				i += identLen(src[i:])
				k = "dimension"
			}
			s := take(i)
			emit(k, s, s, l, c)
			continue
		}
		if identStart(src) {
			i := identLen(src)
			k := "ident"
			if i < len(src) && src[i] == '(' {
				i++
				k = "function"
			}
			s := take(i)
			value := s
			if k == "ident" || k == "function" {
				value = unescapeIdent(strings.TrimSuffix(s, "("))
			}
			if k == "function" && strings.EqualFold(value, "url") {
				// CSS Syntax: unquoted url() consumes to ')' or EOF, recovering bad-url.
				rest := src
				off := 0
				for off < len(rest) && (rest[off] == ' ' || rest[off] == '\n' || rest[off] == '\t') {
					off++
				}
				if off < len(rest) && rest[off] != '\'' && rest[off] != '"' {
					start := off
					bad := false
					for off < len(rest) && rest[off] != ')' {
						if rest[off] == '"' || rest[off] == '\'' || rest[off] == '(' || rest[off] == '\n' {
							bad = true
						}
						if rest[off] == '\\' {
							if off+1 >= len(rest) || rest[off+1] == '\n' {
								bad = true
							} else {
								off++
							}
						}
						off++
					}
					text := strings.TrimSpace(rest[start:off])
					if off < len(rest) {
						off++
					} else {
						bad = true
					}
					s += take(off)
					kind := "url"
					if bad {
						kind = "bad-url"
					}
					emit(kind, s, text, l, c)
					continue
				}
			}
			emit(k, s, value, l, c)
			continue
		}
		s := take(n)
		emit(s, s, s, l, c)
	}
	return out
}
func identStart(s string) bool {
	if len(s) == 0 {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsLetter(r) || r == '_' || r == '-' || r == '\\' || r >= 128
}
func identLen(s string) int {
	i := 0
	for i < len(s) {
		r, n := utf8.DecodeRuneInString(s[i:])
		if r == '\\' {
			i += n
			if i < len(s) {
				_, e := escape(s[i:])
				i += e
			}
			continue
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' && r < 128 {
			break
		}
		i += n
	}
	return i
}
func escape(s string) (rune, int) {
	if len(s) == 0 {
		return '\uFFFD', 0
	}
	i := 0
	var x rune
	for i < len(s) && i < 6 {
		ch := s[i]
		var d byte
		switch {
		case ch >= '0' && ch <= '9':
			d = ch - '0'
		case ch >= 'a' && ch <= 'f':
			d = ch - 'a' + 10
		case ch >= 'A' && ch <= 'F':
			d = ch - 'A' + 10
		default:
			goto done
		}
		x = x*16 + rune(d)
		i++
	}
done:
	if i > 0 {
		if i < len(s) && s[i] == ' ' {
			i++
		}
		if x == 0 || x > utf8.MaxRune || x >= 0xd800 && x <= 0xdfff {
			x = '\uFFFD'
		}
		return x, i
	}
	r, n := utf8.DecodeRuneInString(s)
	return r, n
}
func numberStart(s string) bool {
	if len(s) == 0 {
		return false
	}
	i := 0
	if s[i] == '+' || s[i] == '-' {
		i++
	}
	if i < len(s) && s[i] >= '0' && s[i] <= '9' {
		return true
	}
	return i+1 < len(s) && s[i] == '.' && s[i+1] >= '0' && s[i+1] <= '9'
}
func numberLen(s string) int {
	i := 0
	if s[i] == '+' || s[i] == '-' {
		i++
	}
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		k := j
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j > k {
			i = j
		}
	}
	return i
}

func unescapeIdent(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\\' && i+1 < len(s) {
			r, n := escape(s[i+1:])
			b.WriteRune(r)
			i += n + 1
		} else {
			r, n := utf8.DecodeRuneInString(s[i:])
			b.WriteRune(r)
			i += n
		}
	}
	return b.String()
}
