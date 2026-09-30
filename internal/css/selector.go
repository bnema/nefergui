package css

import "strings"

// StateFlags are the supported CSS pseudo-class states.
type StateFlags uint8

const (
	Hover StateFlags = 1 << iota
	Active
	Focus
	FocusVisible
	Disabled
	Checked
)

// Element is the matching identity for one Compute call. Parentage comes from Computed.
type Element struct {
	Type, ID string
	Classes  []string
	State    StateFlags
	Inline   *Declarations
}
type Specificity struct{ IDs, Classes, Types int }

func (s Specificity) greater(t Specificity) bool {
	if s.IDs != t.IDs {
		return s.IDs > t.IDs
	}
	if s.Classes != t.Classes {
		return s.Classes > t.Classes
	}
	return s.Types > t.Types
}

type compound struct {
	typ, id          string
	classes, pseudos []string
	combinator       string
	typID, idID      uint32
	classIDs         []uint32
	state            StateFlags
}
type Selector struct {
	parts       []compound
	Specificity Specificity
}

func parseSelectors(ts []Token) ([]Selector, bool) {
	var result []Selector
	start := 0
	for i := 0; i <= len(ts); i++ {
		if i == len(ts) || ts[i].Kind == "," {
			s, ok := parseSelector(ts[start:i])
			if !ok {
				return nil, false
			}
			result = append(result, s)
			start = i + 1
		}
	}
	return result, len(result) > 0
}
func parseSelector(ts []Token) (Selector, bool) {
	s := Selector{}
	i := 0
	pending := ""
	for i < len(ts) {
		space := false
		for i < len(ts) && ts[i].Kind == "space" {
			space = true
			i++
		}
		if space && len(s.parts) > 0 && pending == "" {
			pending = " "
		}
		if i >= len(ts) {
			break
		}
		if ts[i].Kind == ">" {
			if len(s.parts) == 0 || pending == ">" {
				return s, false
			}
			pending = ">"
			i++
			continue
		}
		p := compound{combinator: pending}
		pending = ""
		seen := false
		for i < len(ts) {
			t := ts[i]
			if t.Kind == "space" || t.Kind == ">" {
				break
			}
			switch t.Kind {
			case "hash":
				if p.id != "" {
					return s, false
				}
				p.id = t.Value
				s.Specificity.IDs++
				seen = true
				i++
			case "ident":
				if seen || p.typ != "" {
					return s, false
				}
				p.typ = strings.ToLower(t.Value)
				s.Specificity.Types++
				seen = true
				i++
			case ".", "#", ":":
				if i+1 >= len(ts) || ts[i+1].Kind != "ident" {
					return s, false
				}
				v := ts[i+1].Value
				switch t.Kind {
				case ".":
					p.classes = append(p.classes, v)
					s.Specificity.Classes++
				case "#":
					if p.id != "" {
						return s, false
					}
					p.id = v
					s.Specificity.IDs++
				case ":":
					switch strings.ToLower(v) {
					case "hover", "active", "focus", "focus-visible", "disabled", "checked":
						p.pseudos = append(p.pseudos, strings.ToLower(v))
						s.Specificity.Classes++
					default:
						return s, false
					}
				}
				seen = true
				i += 2
			default:
				return s, false
			}
		}
		if !seen || len(s.parts) > 0 && p.combinator == "" {
			return s, false
		}
		s.parts = append(s.parts, p)
	}
	return s, len(s.parts) > 0 && pending != ">"
}
