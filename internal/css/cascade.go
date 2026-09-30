package css

import (
	_ "embed"
	"strings"
)

//go:embed ua.css
var uaSource string
var uaSheet = Parse(uaSource)

// UA returns the embedded user-agent sheet. Treat the returned sheet as immutable.
func UA() Sheet { return uaSheet }

// Declarations are parsed inline CSS. Do not modify them after passing them to Compute.
type Declarations struct {
	compiled []compiledDecl
}

// ParseInline parses a declaration list using the stylesheet declaration grammar.
func ParseInline(src string) (*Declarations, []Diagnostic) {
	s := Parse("app{" + src + "}")
	d := &Declarations{}
	if len(s.Rules) > 0 {
		d.compiled = compileDecls(s.Rules[0].Declarations)
	}
	for i := range s.Diagnostics {
		if s.Diagnostics[i].Line == 1 {
			s.Diagnostics[i].Col -= len("app{")
		}
	}
	return d, s.Diagnostics
}

type compiledDecl struct {
	property  propertyID
	custom    string
	text      string
	value     Value
	parsed    bool
	raw       []Token
	group     *pendingShorthand
	important bool
	order     int
}

// One pending substitution is shared by all of a shorthand's longhand winners.
// Expanding and validating together prevents partial computed values.
type pendingShorthand struct {
	property string
	text     string
	targets  []propertyID
}

type compiledRule struct {
	selector Selector
	decls    []compiledDecl
	origin   int
	right    compound
}
type candidate struct {
	decl          *compiledDecl
	spec          Specificity
	origin, order int
}

func better(a, b candidate) bool {
	if b.decl == nil {
		return true
	}
	if a.decl.important != b.decl.important {
		return a.decl.important
	}
	if a.origin != b.origin {
		return a.origin > b.origin
	}
	if a.spec != b.spec {
		return a.spec.greater(b.spec)
	}
	return a.order >= b.order
}

type memoKey struct {
	parent         *memoNode
	typ, id, class uint32
	state          StateFlags
	inline         *Declarations
}
type memoNode struct {
	key    memoKey
	style  *Style
	custom map[string]string
	root   float64
	used   uint64
}

// Computed holds both the typed style and the identity needed for descendant matching.
type Computed struct {
	Style *Style
	node  *memoNode
}

// Engine is single-goroutine only; neither Compute nor EndFrame is synchronized.
// Compiled rules never change. Compile a new Engine when CSS changes.
type Engine struct {
	ids                   map[string]uint32
	sets                  map[uint64][]classSet
	classMembers          map[uint32][]uint32
	nextID, nextSet       uint32
	usedIDs, usedSets     map[uint32]bool
	rules                 []compiledRule
	byID, byClass, byType map[string][]int
	universal             []int
	memo                  map[memoKey]*Computed
	scratch               []*memoNode
	generation            uint64
}
type classSet struct {
	id      uint32
	members []uint32
}

// Compile indexes the rightmost compound of every selector and freezes the sheets.
func Compile(ua, author Sheet) *Engine {
	en := &Engine{ids: make(map[string]uint32), sets: make(map[uint64][]classSet), classMembers: make(map[uint32][]uint32), usedIDs: make(map[uint32]bool), usedSets: make(map[uint32]bool), byID: make(map[string][]int), byClass: make(map[string][]int), byType: make(map[string][]int), memo: make(map[memoKey]*Computed)}
	order := 0
	for origin, sheet := range []Sheet{ua, author} {
		for _, r := range sheet.Rules {
			ds := compileDecls(r.Declarations)
			for i := range ds {
				ds[i].order += order
			}
			order += len(r.Declarations)
			for _, sel := range r.Selectors {
				sel.parts = append([]compound(nil), sel.parts...)
				for pi := range sel.parts {
					part := &sel.parts[pi]
					part.typID = en.intern(part.typ)
					part.idID = en.intern(part.id)
					for _, cl := range part.classes {
						part.classIDs = append(part.classIDs, en.intern(cl))
					}
					for _, pseudo := range part.pseudos {
						part.state |= pseudoFlag(pseudo)
					}
				}
				i := len(en.rules)
				en.rules = append(en.rules, compiledRule{selector: sel, decls: ds, origin: origin, right: sel.parts[len(sel.parts)-1]})
				right := sel.parts[len(sel.parts)-1]
				switch {
				case right.id != "":
					en.byID[right.id] = append(en.byID[right.id], i)
				case len(right.classes) > 0:
					en.byClass[right.classes[0]] = append(en.byClass[right.classes[0]], i)
				case right.typ != "":
					en.byType[right.typ] = append(en.byType[right.typ], i)
				default:
					en.universal = append(en.universal, i)
				}
			}
		}
	}
	return en
}
func (en *Engine) intern(s string) uint32 {
	if s == "" {
		return 0
	}
	if id := en.ids[s]; id != 0 {
		return id
	}
	en.nextID++
	id := en.nextID
	en.ids[s] = id
	return id
}
func hashClasses(xs []string) uint64 {
	var sum uint64
	for _, x := range xs {
		h := uint64(14695981039346656037)
		for i := 0; i < len(x); i++ {
			h ^= uint64(x[i])
			h *= 1099511628211
		}
		sum += h * 0x9e3779b97f4a7c15
	}
	return sum
}
func (en *Engine) classes(xs []string) uint32 {
	if len(xs) == 0 {
		return 0
	}
	h := hashClasses(xs)
	for _, set := range en.sets[h] {
		if len(set.members) != len(xs) {
			continue
		}
		ok := true
		for _, x := range xs {
			id := en.intern(x)
			found := false
			for _, v := range set.members {
				if v == id {
					found = true
					break
				}
			}
			if !found {
				ok = false
				break
			}
		}
		if ok {
			return set.id
		}
	}
	en.nextSet++
	id := en.nextSet
	s := classSet{id: id, members: make([]uint32, 0, len(xs))}
	for _, x := range xs {
		s.members = append(s.members, en.intern(x))
	}
	en.sets[h] = append(en.sets[h], s)
	en.classMembers[id] = s.members
	return id
}
func pseudoFlag(name string) StateFlags {
	switch name {
	case "hover":
		return Hover
	case "active":
		return Active
	case "focus":
		return Focus
	case "focus-visible":
		return FocusVisible
	case "disabled":
		return Disabled
	case "checked":
		return Checked
	}
	return 0
}
func (en *Engine) matchPart(p compound, k memoKey) bool {
	if p.typID != 0 && p.typID != k.typ || p.idID != 0 && p.idID != k.id || p.state&k.state != p.state {
		return false
	}
	for _, id := range p.classIDs {
		found := false
		for _, member := range en.classMembers[k.class] {
			if member == id {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func (en *Engine) matchesAncestors(s Selector, ancestor *memoNode) bool {
	for i := len(s.parts) - 1; i > 0; {
		comb := s.parts[i].combinator
		i--
		if comb == ">" {
			if ancestor == nil || !en.matchPart(s.parts[i], ancestor.key) {
				return false
			}
			ancestor = ancestor.key.parent
		} else {
			for ancestor != nil && !en.matchPart(s.parts[i], ancestor.key) {
				ancestor = ancestor.key.parent
			}
			if ancestor == nil {
				return false
			}
			ancestor = ancestor.key.parent
		}
	}
	return true
}

// Compute returns the same *Computed on memo hits. parent must come from this Engine.
func (en *Engine) Compute(e *Element, parent *Computed) *Computed {
	var p *memoNode
	if parent != nil {
		p = parent.node
	}
	typ := strings.ToLower(e.Type)
	k := memoKey{parent: p, typ: en.intern(typ), id: en.intern(e.ID), class: en.classes(e.Classes), state: e.State, inline: e.Inline}
	if c := en.memo[k]; c != nil {
		c.node.used = en.generation
		return c
	}
	n := &memoNode{key: k, used: en.generation}
	var winners [propertyCount]candidate
	customWinners := map[string]candidate{}
	visit := func(indices []int) {
		for _, idx := range indices {
			r := &en.rules[idx]
			if !en.matchPart(r.right, k) || len(r.selector.parts) > 1 && !en.matchesAncestors(r.selector, k.parent) {
				continue
			}
			for di := range r.decls {
				d := &r.decls[di]
				c := candidate{decl: d, spec: r.selector.Specificity, origin: r.origin, order: d.order}
				if d.custom != "" {
					if better(c, customWinners[d.custom]) {
						customWinners[d.custom] = c
					}
				} else if better(c, winners[d.property]) {
					winners[d.property] = c
				}
			}
		}
	}
	visit(en.universal)
	visit(en.byType[typ])
	visit(en.byID[e.ID])
	for _, cl := range e.Classes {
		visit(en.byClass[cl])
	}
	if e.Inline != nil {
		for i := range e.Inline.compiled {
			d := &e.Inline.compiled[i]
			c := candidate{decl: d, origin: 2, order: d.order}
			if d.custom != "" {
				if better(c, customWinners[d.custom]) {
					customWinners[d.custom] = c
				}
			} else if better(c, winners[d.property]) {
				winners[d.property] = c
			}
		}
	}
	if p != nil {
		n.custom = p.custom
		n.root = p.root
	}
	if len(customWinners) > 0 {
		n.custom = make(map[string]string, len(customWinners)+len(n.custom))
		if p != nil {
			for key, value := range p.custom {
				n.custom[key] = value
			}
		}
		for key, c := range customWinners {
			n.custom[key] = c.decl.text
		}
	}
	style := new(Style)
	*style = initialStyle
	if p != nil {
		style.Color = p.style.Color
		style.FontFamily = p.style.FontFamily
		style.FontSize = p.style.FontSize
		style.FontWeight = p.style.FontWeight
		style.FontStyle = p.style.FontStyle
		style.LineHeight = p.style.LineHeight
		style.LineHeightNumber = p.style.LineHeightNumber
		style.LineHeightKind = p.style.LineHeightKind
		style.TextAlign = p.style.TextAlign
		style.LetterSpacing = p.style.LetterSpacing
		style.LetterSpacingNormal = p.style.LetterSpacingNormal
		style.Cursor = p.style.Cursor
		style.AccentColor = p.style.AccentColor
	}
	pending := map[*pendingShorthand]shorthandResult{}
	compute := func(id propertyID, font, root float64, current Color) Value {
		name := propertyNames[id]
		c := winners[id]
		if c.decl == nil {
			if isInheritedID(id) && p != nil {
				return p.style.value(id)
			}
			if id == p_color {
				return Value{Color: Color{A: 1}}
			}
			if isCurrentColorDefault(id) {
				return Value{Color: current}
			}
			return defaultValues[id]
		}
		text := c.decl.text
		if text == "inherit" || text == "unset" && isInheritedID(id) {
			if p != nil {
				return p.style.value(id)
			}
			text = initial(name)
		} else if text == "initial" || text == "unset" {
			if id != p_color && id != p_font_size {
				if isCurrentColorDefault(id) {
					return Value{Color: current}
				}
				return defaultValues[id]
			}
			text = initial(name)
		}
		if len(c.decl.raw) > 0 {
			var ok bool
			if c.decl.group != nil {
				g := c.decl.group
				if result, seen := pending[g]; seen {
					ok = result.ok
					if ok {
						text = result.texts[id]
					}
				} else {
					result := resolveShorthand(g, n.custom, current, font, root)
					pending[g] = result
					ok = result.ok
					if ok {
						text = result.texts[id]
					}
				}
			} else {
				text, ok = resolveVars(text, n.custom, map[string]bool{}, 0)
			}

			if !ok {
				if inherited(name) && p != nil {
					return p.style.value(id)
				}
				text = initial(name)
			}
		}
		if text != c.decl.text {
			if text == "inherit" || text == "unset" && isInheritedID(id) {
				if p != nil {
					return p.style.value(id)
				}
				text = initial(name)
			}
			if text == "initial" || text == "unset" {
				text = initial(name)
			}
		}
		if text != c.decl.text && text == initial(name) && id != p_font_size && id != p_color {
			if isCurrentColorDefault(id) {
				return Value{Color: current}
			}
			return defaultValues[id]
		}
		if c.decl.parsed && text == c.decl.text {
			if staticIndependent(name, text) {
				return c.decl.value
			}
			if strings.EqualFold(text, "currentcolor") && (id == p_color || isCurrentColorDefault(id)) {
				return Value{Color: current}
			}
		}
		v, ok := parseValue(name, text, current, font, root)
		if !ok {
			if inherited(name) && p != nil {
				return p.style.value(id)
			}
			if isCurrentColorDefault(id) {
				return Value{Color: current}
			}
			return defaultValues[id]
		}
		return v
	}
	font, root := 16.0, 16.0
	current := Color{A: 1}
	if p != nil {
		font = p.style.FontSize.Value
		root = p.root
		current = p.style.Color
	}
	if winners[p_font_size].decl != nil {
		style.set(p_font_size, compute(p_font_size, font, root, current))
	}
	font = style.FontSize.Value
	if p == nil {
		root = font
	}
	n.root = root
	if winners[p_color].decl != nil {
		style.set(p_color, compute(p_color, font, root, current))
	}
	current = style.Color
	style.BorderColor = ColorSides{current, current, current, current}
	style.OutlineColor = current
	for id := propertyID(0); id < propertyCount; id++ {
		if id != p_color && id != p_font_size && winners[id].decl != nil {
			style.set(id, compute(id, font, root, current))
		}
	}
	n.style = style

	c := &Computed{Style: style, node: n}
	en.memo[k] = c
	return c
}

// EndFrame advances the generation and drops entries not used in the last two frames.
func (en *Engine) EndFrame() {
	en.generation++
	if en.generation < 2 {
		return
	}
	// Each recently used descendant pins its ancestor chain before sweeping.
	live := en.scratch[:0]
	for _, c := range en.memo {
		if c.node.used == en.generation-1 {
			live = append(live, c.node)
		}
	}
	for _, n := range live {
		for p := n.key.parent; p != nil; p = p.key.parent {
			p.used = en.generation - 1
		}
	}
	for k, c := range en.memo {
		if c.node.used+2 <= en.generation {
			delete(en.memo, k)
		}
	}
	// Keep selector IDs and every identity referenced by a surviving memo key.
	usedIDs, usedSets := en.usedIDs, en.usedSets
	clear(usedIDs)
	clear(usedSets)
	for _, r := range en.rules {
		for _, part := range r.selector.parts {
			usedIDs[part.typID], usedIDs[part.idID] = true, true
			for _, id := range part.classIDs {
				usedIDs[id] = true
			}
		}
	}
	for k := range en.memo {
		usedIDs[k.typ], usedIDs[k.id] = true, true
		usedSets[k.class] = true
	}
	for hash, bucket := range en.sets {
		kept := bucket[:0]
		for _, set := range bucket {
			if usedSets[set.id] {
				kept = append(kept, set)
				for _, id := range set.members {
					usedIDs[id] = true
				}
			} else {
				delete(en.classMembers, set.id)
			}
		}
		if len(kept) == 0 {
			delete(en.sets, hash)
		} else {
			en.sets[hash] = kept
		}
	}
	for name, id := range en.ids {
		if !usedIDs[id] {
			delete(en.ids, name)
		}
	}
	clear(live)
	en.scratch = live[:0]
}

// Shorthand targets are the longhands affected even before var() is substituted.
func shorthandTargets(name string) []string {
	switch name {
	case "margin":
		return []string{"margin-top", "margin-right", "margin-bottom", "margin-left"}
	case "padding":
		return []string{"padding-top", "padding-right", "padding-bottom", "padding-left"}
	case "border-width":
		return []string{"border-top-width", "border-right-width", "border-bottom-width", "border-left-width"}
	case "border-style":
		return []string{"border-top-style", "border-right-style", "border-bottom-style", "border-left-style"}
	case "border-color":
		return []string{"border-top-color", "border-right-color", "border-bottom-color", "border-left-color"}
	case "border-radius":
		return []string{"border-top-left-radius", "border-top-right-radius", "border-bottom-right-radius", "border-bottom-left-radius"}
	case "background":
		return []string{"background-color"}
	case "border":
		return []string{"border-top-width", "border-top-style", "border-top-color", "border-right-width", "border-right-style", "border-right-color", "border-bottom-width", "border-bottom-style", "border-bottom-color", "border-left-width", "border-left-style", "border-left-color"}
	case "border-top":
		return []string{"border-top-width", "border-top-style", "border-top-color"}
	case "border-right":
		return []string{"border-right-width", "border-right-style", "border-right-color"}
	case "border-bottom":
		return []string{"border-bottom-width", "border-bottom-style", "border-bottom-color"}
	case "border-left":
		return []string{"border-left-width", "border-left-style", "border-left-color"}
	case "outline":
		return []string{"outline-width", "outline-style", "outline-color"}
	case "flex":
		return []string{"flex-grow", "flex-shrink", "flex-basis"}
	case "overflow":
		return []string{"overflow-x", "overflow-y"}
	case "gap":
		return []string{"row-gap", "column-gap"}
	}
	return nil
}
func staticIndependent(name, text string) bool {
	lower := strings.ToLower(text)
	// em/rem are resolved using the current element/root font. "em" in a font
	// family name does not affect its value but keeping that case dynamic is safe.
	return !strings.Contains(lower, "currentcolor") && !strings.Contains(lower, "em") && name != "font-size" && name != "box-shadow" && name != "line-height"
}

// Initial values independent of color and font metrics, prepared once rather than parsed per node.
var defaultValues = func() [propertyCount]Value {
	var values [propertyCount]Value
	for i := propertyID(0); i < propertyCount; i++ {
		values[i], _ = parseValue(propertyNames[i], initial(propertyNames[i]), Color{A: 1}, 16, 16)
	}
	return values
}()

func isCurrentColorDefault(id propertyID) bool {
	return id == p_outline_color || id == p_border_top_color || id == p_border_right_color || id == p_border_bottom_color || id == p_border_left_color
}

func isInheritedID(id propertyID) bool {
	switch id {
	case p_color, p_font_family, p_font_size, p_font_weight, p_font_style, p_line_height, p_text_align, p_letter_spacing, p_cursor, p_accent_color:
		return true
	}
	return false
}

func compileDecls(declarations []Declaration) []compiledDecl {
	var result []compiledDecl
	for i, d := range declarations {
		if strings.HasPrefix(d.Property, "--") {
			result = append(result, compiledDecl{custom: d.Property, text: d.Value, important: d.Important, order: i})
			continue
		}
		expanded, ok := expand(d)
		if d.Value == "inherit" || d.Value == "initial" || d.Value == "unset" {
			if targets := shorthandTargets(d.Property); len(targets) > 0 {
				expanded = nil
				for _, name := range targets {
					expanded = append(expanded, Declaration{Property: name, Value: d.Value, Important: d.Important})
				}
				ok = true
			}
		}
		hasVar := containsVar(d.Value)
		if targets := shorthandTargets(d.Property); !hasVar && len(targets) > 0 && ok {
			for _, one := range expanded {
				if one.Value != "inherit" && one.Value != "initial" && one.Value != "unset" {
					if _, valid := parseValue(one.Property, one.Value, Color{A: 1}, 16, 16); !valid {
						ok = false
						break
					}
				}
			}
		}
		if targets := shorthandTargets(d.Property); hasVar && len(targets) > 0 {
			expanded = nil
			for _, name := range targets {
				expanded = append(expanded, Declaration{Property: name, Value: d.Value, Important: d.Important})
			}
			ok = true
		}
		if !ok {
			continue
		}
		var group *pendingShorthand
		var groupRaw []Token
		if hasVar && len(shorthandTargets(d.Property)) > 0 {
			groupRaw = Tokenize(d.Value)
			group = &pendingShorthand{property: d.Property, text: d.Value}
			for _, one := range expanded {
				if id, exists := propertyIndex(one.Property); exists {
					group.targets = append(group.targets, id)
				}
			}
		}
		for _, one := range expanded {
			p, exists := propertyIndex(one.Property)
			if !exists {
				continue
			}
			cd := compiledDecl{property: p, text: one.Value, important: one.Important, order: i}
			if hasVar {
				cd.group = group
				if group == nil {
					cd.raw = Tokenize(one.Value)
				} else {
					cd.raw = groupRaw
				}
			}
			if !hasVar && one.Value != "inherit" && one.Value != "initial" && one.Value != "unset" {
				var valid bool
				cd.value, valid = parseValue(one.Property, one.Value, Color{A: 1}, 16, 16)
				if !valid {
					continue
				}
				cd.parsed = true
			}
			result = append(result, cd)
		}
	}
	return result
}

var initialStyle = func() Style {
	var s Style
	for i := propertyID(0); i < propertyCount; i++ {
		s.set(i, defaultValues[i])
	}
	return s
}()

type shorthandResult struct {
	texts map[propertyID]string
	ok    bool
}

func resolveShorthand(g *pendingShorthand, custom map[string]string, current Color, font, root float64) shorthandResult {
	text, ok := resolveVars(g.text, custom, map[string]bool{}, 0)
	if !ok {
		return shorthandResult{}
	}
	result := shorthandResult{texts: make(map[propertyID]string, len(g.targets)), ok: true}
	if text == "inherit" || text == "initial" || text == "unset" {
		for _, id := range g.targets {
			result.texts[id] = text
		}
		return result
	}
	ds, ok := expand(Declaration{Property: g.property, Value: text})
	if !ok {
		return shorthandResult{}
	}
	for _, d := range ds {
		id, exists := propertyIndex(d.Property)
		if !exists {
			return shorthandResult{}
		}
		if _, valid := parseValue(d.Property, d.Value, current, font, root); !valid {
			return shorthandResult{}
		}
		result.texts[id] = d.Value
	}
	for _, id := range g.targets {
		if _, exists := result.texts[id]; !exists {
			return shorthandResult{}
		}
	}
	return result
}
