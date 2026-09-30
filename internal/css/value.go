package css

import (
	"math"
	"strconv"
	"strings"
)

type Length struct {
	Value float64
	Unit  string
}
type Color struct{ R, G, B, A float64 }
type Shadow struct {
	Inset              bool
	X, Y, Blur, Spread Length
	Color              Color
}
type Value struct {
	Length   Length
	Color    Color
	Number   float64
	Keyword  string
	Families []string
	Shadows  []Shadow
}

func finite(s string) (float64, bool) {
	v, e := strconv.ParseFloat(s, 64)
	return v, e == nil && !math.IsNaN(v) && !math.IsInf(v, 0)
}
func length(s string, negative, auto bool) (Length, bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	if auto && s == "auto" {
		return Length{Unit: "auto"}, true
	}
	for _, unit := range []string{"rem", "em", "px", "%"} {
		if strings.HasSuffix(s, unit) {
			v, ok := finite(strings.TrimSuffix(s, unit))
			return Length{v, unit}, ok && (negative || v >= 0)
		}
	}
	v, ok := finite(s)
	return Length{v, "px"}, ok && v == 0
}
func resolveLen(l Length, font, root float64) Length {
	switch l.Unit {
	case "em":
		l.Value *= font
		l.Unit = "px"
	case "rem":
		l.Value *= root
		l.Unit = "px"
	}
	return l
}
func validLength(l Length) bool    { return !math.IsNaN(l.Value) && !math.IsInf(l.Value, 0) }
func rgb(r, g, b, a float64) Color { return Color{clamp(r), clamp(g), clamp(b), clamp(a)} }
func clamp(x float64) float64      { return math.Max(0, math.Min(1, x)) }
func parseColor(s string, current Color) (Color, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "currentcolor" {
		return current, true
	}
	if s == "transparent" {
		return Color{}, true
	}
	if strings.HasPrefix(s, "#") {
		h := s[1:]
		if len(h) != 3 && len(h) != 4 && len(h) != 6 && len(h) != 8 {
			return Color{}, false
		}
		var b [4]uint64
		b[3] = 255
		short := len(h) <= 4
		n := 3
		if len(h) == 4 || len(h) == 8 {
			n = 4
		}
		for i := 0; i < n; i++ {
			part := ""
			if short {
				part = strings.Repeat(string(h[i]), 2)
			} else {
				part = h[i*2 : i*2+2]
			}
			v, e := strconv.ParseUint(part, 16, 8)
			if e != nil {
				return Color{}, false
			}
			b[i] = v
		}
		return rgb(float64(b[0])/255, float64(b[1])/255, float64(b[2])/255, float64(b[3])/255), true
	}
	open := strings.IndexByte(s, '(')
	if open < 0 || !strings.HasSuffix(s, ")") {
		if v, ok := namedColors[s]; ok {
			return v, true
		}
		return Color{}, false
	}
	fn := s[:open]
	if fn != "rgb" && fn != "rgba" && fn != "hsl" && fn != "hsla" {
		return Color{}, false
	}
	inner := strings.TrimSpace(s[open+1 : len(s)-1])
	comma := strings.Contains(inner, ",")
	var args []string
	if comma {
		args = strings.Split(inner, ",")
	} else {
		inner = strings.ReplaceAll(inner, "/", " / ")
		args = strings.Fields(inner)
	}
	for i := range args {
		args[i] = strings.TrimSpace(args[i])
	}
	if !comma && len(args) == 5 && args[3] == "/" {
		args = append(args[:3], args[4])
	}
	if len(args) < 3 || len(args) > 4 || comma && strings.Contains(inner, "/") {
		return Color{}, false
	}
	if (fn == "rgba" || fn == "hsla") && len(args) != 4 {
		return Color{}, false
	}
	a := 1.0
	if len(args) == 4 {
		v, ok := channel(args[3], 1)
		if !ok {
			return Color{}, false
		}
		a = v
	}
	if fn == "rgb" || fn == "rgba" {
		var ch [3]float64
		for i := 0; i < 3; i++ {
			v, ok := channel(args[i], 255)
			if !ok {
				return Color{}, false
			}
			ch[i] = v
		}
		return rgb(ch[0], ch[1], ch[2], a), true
	}
	hue := strings.TrimSuffix(args[0], "deg")
	h, ok := finite(hue)
	if !ok {
		return Color{}, false
	}
	sat, ok := percent(args[1])
	if !ok {
		return Color{}, false
	}
	light, ok := percent(args[2])
	if !ok {
		return Color{}, false
	}
	h = math.Mod(h, 360)
	if h < 0 {
		h += 360
	}
	sat = clamp(sat)
	light = clamp(light)
	c := (1 - math.Abs(2*light-1)) * sat
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := light - c/2
	var r, g, b float64
	switch {
	case h < 60:
		r, g = c, x
	case h < 120:
		r, g = x, c
	case h < 180:
		g, b = c, x
	case h < 240:
		g, b = x, c
	case h < 300:
		r, b = x, c
	default:
		r, b = c, x
	}
	return rgb(r+m, g+m, b+m, a), true
}
func percent(s string) (float64, bool) {
	if !strings.HasSuffix(s, "%") {
		return 0, false
	}
	v, ok := finite(strings.TrimSuffix(s, "%"))
	return v / 100, ok
}
func channel(s string, scale float64) (float64, bool) {
	if strings.HasSuffix(s, "%") {
		return percent(s)
	}
	v, ok := finite(s)
	return v / scale, ok
}

// splitTopLevel separates commas only outside functions and quoted strings.
func splitTopLevel(s string) []string {
	var parts []string
	start, depth := 0, 0
	pos := 0
	for _, t := range Tokenize(s) {
		if t.Kind == "," && depth == 0 {
			parts = append(parts, s[start:pos])
			start = pos + len(t.Raw)
		}
		if t.Kind == "function" || t.Kind == "(" {
			depth++
		}
		if t.Kind == ")" {
			depth--
		}
		pos += len(t.Raw)
	}
	parts = append(parts, s[start:])
	return parts
}

func fields(s string) []string {
	ts := Tokenize(s)
	var parts []string
	var buf strings.Builder
	depth := 0
	for _, t := range ts {
		if t.Kind == "function" {
			depth++
		}
		if t.Kind == ")" {
			depth--
		}
		if t.Kind == "space" && depth == 0 {
			if buf.Len() > 0 {
				parts = append(parts, buf.String())
				buf.Reset()
			}
			continue
		}
		buf.WriteString(t.Raw)
	}
	if buf.Len() > 0 {
		parts = append(parts, buf.String())
	}
	return parts
}
func sides(xs []string) ([]string, bool) {
	if len(xs) < 1 || len(xs) > 4 {
		return nil, false
	}
	switch len(xs) {
	case 1:
		return []string{xs[0], xs[0], xs[0], xs[0]}, true
	case 2:
		return []string{xs[0], xs[1], xs[0], xs[1]}, true
	case 3:
		return []string{xs[0], xs[1], xs[2], xs[1]}, true
	default:
		return xs, true
	}
}

var sideNames = []string{"top", "right", "bottom", "left"}
var cornerNames = []string{"top-left", "top-right", "bottom-right", "bottom-left"}

func expand(d Declaration) ([]Declaration, bool) {
	n := d.Property
	makeD := func(name, value string) Declaration { return Declaration{name, value, d.Important, d.Line, d.Col} }
	v := d.Value
	if n == "background" {
		return []Declaration{makeD("background-color", v)}, true
	}
	for _, prefix := range []string{"margin", "padding", "border-width", "border-style", "border-color", "border-radius"} {
		if n == prefix {
			xs, ok := sides(fields(v))
			if !ok {
				return nil, false
			}
			var ds []Declaration
			for i, x := range xs {
				name := prefix + "-" + sideNames[i]
				if prefix == "border-width" || prefix == "border-style" || prefix == "border-color" {
					name = "border-" + sideNames[i] + "-" + strings.TrimPrefix(prefix, "border-")
				}
				if prefix == "border-radius" {
					name = "border-" + cornerNames[i] + "-radius"
				}
				ds = append(ds, makeD(name, x))
			}
			return ds, true
		}
	}
	if n == "gap" {
		xs := fields(v)
		if len(xs) < 1 || len(xs) > 2 {
			return nil, false
		}
		if len(xs) == 1 {
			xs = append(xs, xs[0])
		}
		return []Declaration{makeD("row-gap", xs[0]), makeD("column-gap", xs[1])}, true
	}
	if n == "overflow" {
		xs := fields(v)
		if len(xs) < 1 || len(xs) > 2 {
			return nil, false
		}
		if len(xs) == 1 {
			xs = append(xs, xs[0])
		}
		return []Declaration{makeD("overflow-x", xs[0]), makeD("overflow-y", xs[1])}, true
	}
	if n == "flex" {
		xs := fields(v)
		if v == "none" {
			xs = []string{"0", "0", "auto"}
		} else if v == "auto" {
			xs = []string{"1", "1", "auto"}
		} else {
			if len(xs) < 1 || len(xs) > 3 {
				return nil, false
			}
			if _, ok := finite(xs[0]); !ok {
				return nil, false
			}
			if len(xs) == 1 {
				xs = append(xs, "1", "0%")
			} else if len(xs) == 2 {
				if _, ok := finite(xs[1]); ok {
					xs = append(xs, "0%")
				} else {
					xs = []string{xs[0], "1", xs[1]}
				}
			}
		}
		return []Declaration{makeD("flex-grow", xs[0]), makeD("flex-shrink", xs[1]), makeD("flex-basis", xs[2])}, true
	}
	if n == "border" || n == "outline" || strings.HasPrefix(n, "border-") && (n == "border-top" || n == "border-right" || n == "border-bottom" || n == "border-left") {
		xs := fields(v)
		width, style, color := "0", "none", "currentColor"
		used := [3]bool{}
		for _, x := range xs {
			if _, ok := length(x, false, false); ok && !used[0] {
				width = x
				used[0] = true
				continue
			}
			if (x == "solid" || x == "none") && !used[1] {
				style = x
				used[1] = true
				continue
			}
			if _, ok := parseColor(x, Color{}); ok && !used[2] {
				color = x
				used[2] = true
				continue
			}
			return nil, false
		}
		if len(xs) == 0 {
			return nil, false
		}
		targets := []string{n}
		if n == "border" {
			targets = []string{"border-top", "border-right", "border-bottom", "border-left"}
		}
		var ds []Declaration
		for _, t := range targets {
			ds = append(ds, makeD(t+"-width", width), makeD(t+"-style", style), makeD(t+"-color", color))
		}
		return ds, true
	}
	return []Declaration{d}, true
}
func inherited(name string) bool {
	switch name {
	case "color", "font-family", "font-size", "font-weight", "font-style", "line-height", "text-align", "letter-spacing", "cursor", "accent-color":
		return true
	}
	return false
}
func initial(name string) string {
	switch name {
	case "color":
		return "black"
	case "accent-color":
		return "#3584e4"
	case "opacity", "flex-shrink":
		return "1"
	case "background-color":
		return "transparent"
	case "box-shadow":
		return "none"
	case "width", "height", "min-width", "min-height", "max-width", "max-height", "flex-basis", "align-self", "cursor":
		return "auto"
	case "box-sizing":
		return "content-box"
	case "display":
		return "block"
	case "flex-direction":
		return "row"
	case "flex-wrap":
		return "nowrap"
	case "justify-content":
		return "flex-start"
	case "align-items":
		return "stretch"
	case "overflow-x", "overflow-y":
		return "visible"
	case "font-family":
		return "sans-serif"
	case "font-size":
		return "16px"
	case "font-weight":
		return "400"
	case "font-style", "letter-spacing", "line-height":
		return "normal"
	case "text-align":
		return "start"
	}
	if strings.HasSuffix(name, "-color") {
		return "currentColor"
	}
	if strings.HasSuffix(name, "-style") {
		return "none"
	}
	return "0"
}
func parseValue(name, s string, current Color, font, root float64) (Value, bool) {
	var out Value
	s = strings.TrimSpace(s)
	lower := strings.ToLower(s)
	if name == "color" || name == "background-color" || strings.HasSuffix(name, "-color") {
		var ok bool
		out.Color, ok = parseColor(s, current)
		return out, ok
	}
	switch name {
	case "opacity", "flex-grow", "flex-shrink":
		v, ok := finite(s)
		if !ok || v < 0 || name == "opacity" && v > 1 {
			return out, false
		}
		out.Number = v
		return out, true
	case "font-weight":
		if lower == "normal" {
			out.Number = 400
			return out, true
		}
		if lower == "bold" {
			out.Number = 700
			return out, true
		}
		v, ok := finite(s)
		if ok && v >= 100 && v <= 900 && math.Mod(v, 100) == 0 {
			out.Number = v
			return out, true
		}
		return out, false
	case "font-family":
		for _, p := range strings.Split(s, ",") {
			p = strings.TrimSpace(p)
			if p == "" {
				return out, false
			}
			if strings.HasPrefix(p, "\"") || strings.HasPrefix(p, "'") {
				ts := Tokenize(p)
				if len(ts) != 1 || ts[0].Kind != "string" {
					return out, false
				}
				p = ts[0].Value
			}
			out.Families = append(out.Families, p)
		}
		return out, len(out.Families) > 0
	case "box-shadow":
		if lower == "none" {
			return out, true
		}
		for _, part := range splitTopLevel(s) {
			xs := fields(part)
			sh := Shadow{Color: current}
			var ls []Length
			for _, x := range xs {
				if x == "inset" && !sh.Inset {
					sh.Inset = true
					continue
				}
				if l, ok := length(x, true, false); ok {
					ls = append(ls, resolveLen(l, font, root))
					continue
				}
				if c, ok := parseColor(x, current); ok {
					sh.Color = c
					continue
				}
				return out, false
			}
			if len(ls) < 2 || len(ls) > 4 {
				return out, false
			}
			for _, l := range ls {
				if !validLength(l) {
					return out, false
				}
			}
			sh.X, sh.Y = ls[0], ls[1]
			if len(ls) > 2 {
				sh.Blur = ls[2]
				if sh.Blur.Value < 0 {
					return out, false
				}
			}
			if len(ls) > 3 {
				sh.Spread = ls[3]
			}
			out.Shadows = append(out.Shadows, sh)
		}
		return out, true
	case "font-size":
		l, ok := length(s, false, false)
		if !ok {
			return out, false
		}
		switch l.Unit {
		case "%":
			l.Value = font * l.Value / 100
			l.Unit = "px"
		case "em":
			l.Value *= font
			l.Unit = "px"
		case "rem":
			l.Value *= root
			l.Unit = "px"
		}
		out.Length = l
		return out, validLength(l)
	case "line-height":
		if lower == "normal" {
			out.Keyword = lower
			return out, true
		}
		if v, ok := finite(s); ok && v >= 0 {
			out.Number = v
			out.Keyword = "number"
			return out, true
		}
	}
	lengthName := false
	negative := false
	auto := false
	switch {
	case name == "width" || name == "height" || name == "min-width" || name == "min-height" || name == "max-width" || name == "max-height" || name == "flex-basis":
		lengthName = true
		auto = true
	case name == "outline-offset" || name == "letter-spacing" || strings.HasPrefix(name, "margin-"):
		lengthName = true
		negative = true
		auto = strings.HasPrefix(name, "margin-")
	case name == "font-size" || name == "line-height" || name == "row-gap" || name == "column-gap" || strings.HasPrefix(name, "padding-") || strings.HasSuffix(name, "-width") || strings.HasSuffix(name, "-radius"):
		lengthName = true
	}
	if name == "letter-spacing" && lower == "normal" {
		out.Keyword = lower
		return out, true
	}
	if lengthName {
		l, ok := length(s, negative, auto)
		if name == "outline-offset" || name == "letter-spacing" {
			ok = ok && l.Unit != "%"
		}
		out.Length = resolveLen(l, font, root)
		return out, ok && validLength(out.Length)
	}
	valid := map[string]string{"box-sizing": "content-box border-box", "display": "flex block none", "flex-direction": "row column", "flex-wrap": "nowrap wrap", "justify-content": "flex-start flex-end center space-between space-around space-evenly", "align-items": "stretch flex-start flex-end center", "align-self": "auto stretch flex-start flex-end center", "overflow-x": "visible hidden scroll auto", "overflow-y": "visible hidden scroll auto", "font-style": "normal italic oblique", "text-align": "start end left right center", "cursor": "auto default pointer text not-allowed"}
	if name == "outline-style" || strings.HasPrefix(name, "border-") && strings.HasSuffix(name, "-style") {
		valid[name] = "none solid"
	}
	for _, v := range strings.Fields(valid[name]) {
		if lower == v {
			out.Keyword = v
			return out, true
		}
	}
	return out, false
}
func containsVar(s string) bool {
	for _, t := range Tokenize(s) {
		if t.Kind == "function" && strings.EqualFold(t.Value, "var") {
			return true
		}
	}
	return false
}

func resolveVars(s string, custom map[string]string, active map[string]bool, depth int) (string, bool) {
	if strings.Contains(s, "/*") {
		s = raw(Tokenize(s))
	}
	if depth > 64 {
		return "", false
	}
	var out strings.Builder
	for {
		// Function tokens, unlike byte substrings, cannot occur inside strings or identifiers.
		ts := Tokenize(s)
		start, end, pos, depth, functionLen := -1, -1, 0, 0, 0
		for _, t := range ts {
			if start < 0 {
				if t.Kind == "function" && strings.EqualFold(t.Value, "var") {
					start = pos + len(t.Raw)
					functionLen = len(t.Raw)
				}
			} else {
				if t.Kind == "function" || t.Kind == "(" {
					depth++
				}
				if t.Kind == ")" {
					if depth == 0 {
						end = pos
						break
					}
					depth--
				}
			}
			pos += len(t.Raw)
		}
		if start < 0 {
			out.WriteString(s)
			return out.String(), true
		}
		if end < 0 {
			return "", false
		}
		out.WriteString(s[:start-functionLen])
		inside := s[start:end]
		s = s[end+1:]
		parts := splitTopLevel(inside)
		if len(parts) > 2 {
			parts[1] = strings.Join(parts[1:], ",")
		}
		name := strings.TrimSpace(parts[0])
		if !strings.HasPrefix(name, "--") {
			return "", false
		}
		v, ok := custom[name]
		if ok && !active[name] {
			active[name] = true
			v, ok = resolveVars(v, custom, active, depth+1)
			delete(active, name)
		} else {
			ok = false
		}
		if !ok && len(parts) > 1 {
			v, ok = resolveVars(strings.TrimSpace(parts[1]), custom, active, depth+1)
		}
		if !ok {
			return "", false
		}
		out.WriteString(v)
	}
}
