package css

// Keyword is a computed CSS enumerated value. The documented property grammar
// supplies the possible values for each field.
type Keyword uint8

const (
	KeywordNone Keyword = iota
	KeywordAuto
	KeywordBlock
	KeywordFlex
	KeywordContentBox
	KeywordBorderBox
	KeywordRow
	KeywordColumn
	KeywordNowrap
	KeywordWrap
	KeywordFlexStart
	KeywordFlexEnd
	KeywordCenter
	KeywordSpaceBetween
	KeywordSpaceAround
	KeywordSpaceEvenly
	KeywordStretch
	KeywordVisible
	KeywordHidden
	KeywordScroll
	KeywordNormal
	KeywordItalic
	KeywordOblique
	KeywordStart
	KeywordEnd
	KeywordLeft
	KeywordRight
	KeywordDefault
	KeywordPointer
	KeywordText
	KeywordNotAllowed
	KeywordSolid
	KeywordNumber
)

var keywordNames = []string{"none", "auto", "block", "flex", "content-box", "border-box", "row", "column", "nowrap", "wrap", "flex-start", "flex-end", "center", "space-between", "space-around", "space-evenly", "stretch", "visible", "hidden", "scroll", "normal", "italic", "oblique", "start", "end", "left", "right", "default", "pointer", "text", "not-allowed", "solid", "number"}

func keyword(s string) Keyword {
	for i, k := range keywordNames {
		if k == s {
			return Keyword(i)
		}
	}
	return KeywordNone
}

// Sides and Corners follow clockwise CSS order.
type LengthSides struct{ Top, Right, Bottom, Left Length }
type ColorSides struct{ Top, Right, Bottom, Left Color }
type KeywordSides struct{ Top, Right, Bottom, Left Keyword }
type Radii struct{ TopLeft, TopRight, BottomRight, BottomLeft Length }

// Style is a computed, immutable value. Slice backing arrays are shared: do not
// mutate a returned Style or any of its slices.
type Style struct {
	Color                                                       Color
	Opacity                                                     float64
	BackgroundColor                                             Color
	BorderWidth                                                 LengthSides
	BorderStyle                                                 KeywordSides
	BorderColor                                                 ColorSides
	BorderRadius                                                Radii
	OutlineWidth                                                Length
	OutlineStyle                                                Keyword
	OutlineColor                                                Color
	OutlineOffset                                               Length
	BoxShadow                                                   []Shadow
	Margin, Padding                                             LengthSides
	Width, Height, MinWidth, MinHeight, MaxWidth, MaxHeight     Length
	BoxSizing, Display, FlexDirection, FlexWrap                 Keyword
	FlexGrow, FlexShrink                                        float64
	FlexBasis, RowGap, ColumnGap                                Length
	JustifyContent, AlignItems, AlignSelf, OverflowX, OverflowY Keyword
	FontFamily                                                  []string
	FontSize                                                    Length
	FontWeight                                                  float64
	FontStyle                                                   Keyword
	LineHeight                                                  Length
	LineHeightNumber                                            float64
	LineHeightKind                                              Keyword
	TextAlign                                                   Keyword
	LetterSpacing                                               Length
	LetterSpacingNormal                                         bool
	Cursor                                                      Keyword
	AccentColor                                                 Color // checkbox, radio and slider indicators
}

// propertyID indexes the fixed cascade winner array.
type propertyID uint8

const (
	p_color propertyID = iota
	p_opacity
	p_background_color
	p_outline_width
	p_outline_style
	p_outline_color
	p_outline_offset
	p_box_shadow
	p_width
	p_height
	p_min_width
	p_min_height
	p_max_width
	p_max_height
	p_box_sizing
	p_display
	p_flex_direction
	p_flex_wrap
	p_flex_grow
	p_flex_shrink
	p_flex_basis
	p_row_gap
	p_column_gap
	p_justify_content
	p_align_items
	p_align_self
	p_overflow_x
	p_overflow_y
	p_font_family
	p_font_size
	p_font_weight
	p_font_style
	p_line_height
	p_text_align
	p_letter_spacing
	p_cursor
	p_margin_top
	p_padding_top
	p_border_top_width
	p_border_top_style
	p_border_top_color
	p_margin_right
	p_padding_right
	p_border_right_width
	p_border_right_style
	p_border_right_color
	p_margin_bottom
	p_padding_bottom
	p_border_bottom_width
	p_border_bottom_style
	p_border_bottom_color
	p_margin_left
	p_padding_left
	p_border_left_width
	p_border_left_style
	p_border_left_color
	p_border_top_left_radius
	p_border_top_right_radius
	p_border_bottom_right_radius
	p_border_bottom_left_radius
	p_accent_color
	propertyCount
)

var propertyNames = [...]string{"color", "opacity", "background-color", "outline-width", "outline-style", "outline-color", "outline-offset", "box-shadow", "width", "height", "min-width", "min-height", "max-width", "max-height", "box-sizing", "display", "flex-direction", "flex-wrap", "flex-grow", "flex-shrink", "flex-basis", "row-gap", "column-gap", "justify-content", "align-items", "align-self", "overflow-x", "overflow-y", "font-family", "font-size", "font-weight", "font-style", "line-height", "text-align", "letter-spacing", "cursor", "margin-top", "padding-top", "border-top-width", "border-top-style", "border-top-color", "margin-right", "padding-right", "border-right-width", "border-right-style", "border-right-color", "margin-bottom", "padding-bottom", "border-bottom-width", "border-bottom-style", "border-bottom-color", "margin-left", "padding-left", "border-left-width", "border-left-style", "border-left-color", "border-top-left-radius", "border-top-right-radius", "border-bottom-right-radius", "border-bottom-left-radius", "accent-color"}

func propertyIndex(n string) (propertyID, bool) {
	switch n {
	case "color":
		return p_color, true
	case "opacity":
		return p_opacity, true
	case "background-color":
		return p_background_color, true
	case "outline-width":
		return p_outline_width, true
	case "outline-style":
		return p_outline_style, true
	case "outline-color":
		return p_outline_color, true
	case "outline-offset":
		return p_outline_offset, true
	case "box-shadow":
		return p_box_shadow, true
	case "width":
		return p_width, true
	case "height":
		return p_height, true
	case "min-width":
		return p_min_width, true
	case "min-height":
		return p_min_height, true
	case "max-width":
		return p_max_width, true
	case "max-height":
		return p_max_height, true
	case "box-sizing":
		return p_box_sizing, true
	case "display":
		return p_display, true
	case "flex-direction":
		return p_flex_direction, true
	case "flex-wrap":
		return p_flex_wrap, true
	case "flex-grow":
		return p_flex_grow, true
	case "flex-shrink":
		return p_flex_shrink, true
	case "flex-basis":
		return p_flex_basis, true
	case "row-gap":
		return p_row_gap, true
	case "column-gap":
		return p_column_gap, true
	case "justify-content":
		return p_justify_content, true
	case "align-items":
		return p_align_items, true
	case "align-self":
		return p_align_self, true
	case "overflow-x":
		return p_overflow_x, true
	case "overflow-y":
		return p_overflow_y, true
	case "font-family":
		return p_font_family, true
	case "font-size":
		return p_font_size, true
	case "font-weight":
		return p_font_weight, true
	case "font-style":
		return p_font_style, true
	case "line-height":
		return p_line_height, true
	case "text-align":
		return p_text_align, true
	case "letter-spacing":
		return p_letter_spacing, true
	case "cursor":
		return p_cursor, true
	case "margin-top":
		return p_margin_top, true
	case "padding-top":
		return p_padding_top, true
	case "border-top-width":
		return p_border_top_width, true
	case "border-top-style":
		return p_border_top_style, true
	case "border-top-color":
		return p_border_top_color, true
	case "margin-right":
		return p_margin_right, true
	case "padding-right":
		return p_padding_right, true
	case "border-right-width":
		return p_border_right_width, true
	case "border-right-style":
		return p_border_right_style, true
	case "border-right-color":
		return p_border_right_color, true
	case "margin-bottom":
		return p_margin_bottom, true
	case "padding-bottom":
		return p_padding_bottom, true
	case "border-bottom-width":
		return p_border_bottom_width, true
	case "border-bottom-style":
		return p_border_bottom_style, true
	case "border-bottom-color":
		return p_border_bottom_color, true
	case "margin-left":
		return p_margin_left, true
	case "padding-left":
		return p_padding_left, true
	case "border-left-width":
		return p_border_left_width, true
	case "border-left-style":
		return p_border_left_style, true
	case "border-left-color":
		return p_border_left_color, true
	case "border-top-left-radius":
		return p_border_top_left_radius, true
	case "border-top-right-radius":
		return p_border_top_right_radius, true
	case "border-bottom-right-radius":
		return p_border_bottom_right_radius, true
	case "border-bottom-left-radius":
		return p_border_bottom_left_radius, true
	case "accent-color":
		return p_accent_color, true
	}
	return 0, false
}
func (s *Style) set(n propertyID, v Value) {
	switch n {
	case p_color:
		s.Color = v.Color
	case p_opacity:
		s.Opacity = v.Number
	case p_background_color:
		s.BackgroundColor = v.Color
	case p_outline_width:
		s.OutlineWidth = v.Length
	case p_outline_style:
		s.OutlineStyle = keyword(v.Keyword)
	case p_outline_color:
		s.OutlineColor = v.Color
	case p_outline_offset:
		s.OutlineOffset = v.Length
	case p_box_shadow:
		s.BoxShadow = v.Shadows
	case p_width:
		s.Width = v.Length
	case p_height:
		s.Height = v.Length
	case p_min_width:
		s.MinWidth = v.Length
	case p_min_height:
		s.MinHeight = v.Length
	case p_max_width:
		s.MaxWidth = v.Length
	case p_max_height:
		s.MaxHeight = v.Length
	case p_box_sizing:
		s.BoxSizing = keyword(v.Keyword)
	case p_display:
		s.Display = keyword(v.Keyword)
	case p_flex_direction:
		s.FlexDirection = keyword(v.Keyword)
	case p_flex_wrap:
		s.FlexWrap = keyword(v.Keyword)
	case p_flex_grow:
		s.FlexGrow = v.Number
	case p_flex_shrink:
		s.FlexShrink = v.Number
	case p_flex_basis:
		s.FlexBasis = v.Length
	case p_row_gap:
		s.RowGap = v.Length
	case p_column_gap:
		s.ColumnGap = v.Length
	case p_justify_content:
		s.JustifyContent = keyword(v.Keyword)
	case p_align_items:
		s.AlignItems = keyword(v.Keyword)
	case p_align_self:
		s.AlignSelf = keyword(v.Keyword)
	case p_overflow_x:
		s.OverflowX = keyword(v.Keyword)
	case p_overflow_y:
		s.OverflowY = keyword(v.Keyword)
	case p_font_family:
		s.FontFamily = v.Families
	case p_font_size:
		s.FontSize = v.Length
	case p_font_weight:
		s.FontWeight = v.Number
	case p_font_style:
		s.FontStyle = keyword(v.Keyword)
	case p_line_height:
		s.LineHeight = v.Length
		s.LineHeightKind = keyword(v.Keyword)
		s.LineHeightNumber = v.Number
	case p_text_align:
		s.TextAlign = keyword(v.Keyword)
	case p_letter_spacing:
		s.LetterSpacing = v.Length
		s.LetterSpacingNormal = v.Keyword == "normal"
	case p_cursor:
		s.Cursor = keyword(v.Keyword)
	case p_margin_top:
		s.Margin.Top = v.Length
	case p_padding_top:
		s.Padding.Top = v.Length
	case p_border_top_width:
		s.BorderWidth.Top = v.Length
	case p_border_top_style:
		s.BorderStyle.Top = keyword(v.Keyword)
	case p_border_top_color:
		s.BorderColor.Top = v.Color
	case p_margin_right:
		s.Margin.Right = v.Length
	case p_padding_right:
		s.Padding.Right = v.Length
	case p_border_right_width:
		s.BorderWidth.Right = v.Length
	case p_border_right_style:
		s.BorderStyle.Right = keyword(v.Keyword)
	case p_border_right_color:
		s.BorderColor.Right = v.Color
	case p_margin_bottom:
		s.Margin.Bottom = v.Length
	case p_padding_bottom:
		s.Padding.Bottom = v.Length
	case p_border_bottom_width:
		s.BorderWidth.Bottom = v.Length
	case p_border_bottom_style:
		s.BorderStyle.Bottom = keyword(v.Keyword)
	case p_border_bottom_color:
		s.BorderColor.Bottom = v.Color
	case p_margin_left:
		s.Margin.Left = v.Length
	case p_padding_left:
		s.Padding.Left = v.Length
	case p_border_left_width:
		s.BorderWidth.Left = v.Length
	case p_border_left_style:
		s.BorderStyle.Left = keyword(v.Keyword)
	case p_border_left_color:
		s.BorderColor.Left = v.Color
	case p_border_top_left_radius:
		s.BorderRadius.TopLeft = v.Length
	case p_border_top_right_radius:
		s.BorderRadius.TopRight = v.Length
	case p_border_bottom_right_radius:
		s.BorderRadius.BottomRight = v.Length
	case p_border_bottom_left_radius:
		s.BorderRadius.BottomLeft = v.Length
	case p_accent_color:
		s.AccentColor = v.Color
	}
}
func (s *Style) value(n propertyID) Value {
	var v Value
	switch n {
	case p_color:
		v.Color = s.Color
	case p_opacity:
		v.Number = s.Opacity
	case p_background_color:
		v.Color = s.BackgroundColor
	case p_outline_width:
		v.Length = s.OutlineWidth
	case p_outline_style:
		v.Keyword = keywordNames[s.OutlineStyle]
	case p_outline_color:
		v.Color = s.OutlineColor
	case p_outline_offset:
		v.Length = s.OutlineOffset
	case p_box_shadow:
		v.Shadows = s.BoxShadow
	case p_width:
		v.Length = s.Width
	case p_height:
		v.Length = s.Height
	case p_min_width:
		v.Length = s.MinWidth
	case p_min_height:
		v.Length = s.MinHeight
	case p_max_width:
		v.Length = s.MaxWidth
	case p_max_height:
		v.Length = s.MaxHeight
	case p_box_sizing:
		v.Keyword = keywordNames[s.BoxSizing]
	case p_display:
		v.Keyword = keywordNames[s.Display]
	case p_flex_direction:
		v.Keyword = keywordNames[s.FlexDirection]
	case p_flex_wrap:
		v.Keyword = keywordNames[s.FlexWrap]
	case p_flex_grow:
		v.Number = s.FlexGrow
	case p_flex_shrink:
		v.Number = s.FlexShrink
	case p_flex_basis:
		v.Length = s.FlexBasis
	case p_row_gap:
		v.Length = s.RowGap
	case p_column_gap:
		v.Length = s.ColumnGap
	case p_justify_content:
		v.Keyword = keywordNames[s.JustifyContent]
	case p_align_items:
		v.Keyword = keywordNames[s.AlignItems]
	case p_align_self:
		v.Keyword = keywordNames[s.AlignSelf]
	case p_overflow_x:
		v.Keyword = keywordNames[s.OverflowX]
	case p_overflow_y:
		v.Keyword = keywordNames[s.OverflowY]
	case p_font_family:
		v.Families = s.FontFamily
	case p_font_size:
		v.Length = s.FontSize
	case p_font_weight:
		v.Number = s.FontWeight
	case p_font_style:
		v.Keyword = keywordNames[s.FontStyle]
	case p_line_height:
		v.Length = s.LineHeight
		v.Keyword = keywordNames[s.LineHeightKind]
		v.Number = s.LineHeightNumber
	case p_text_align:
		v.Keyword = keywordNames[s.TextAlign]
	case p_letter_spacing:
		v.Length = s.LetterSpacing
		if s.LetterSpacingNormal {
			v.Keyword = "normal"
		}
	case p_cursor:
		v.Keyword = keywordNames[s.Cursor]
	case p_margin_top:
		v.Length = s.Margin.Top
	case p_padding_top:
		v.Length = s.Padding.Top
	case p_border_top_width:
		v.Length = s.BorderWidth.Top
	case p_border_top_style:
		v.Keyword = keywordNames[s.BorderStyle.Top]
	case p_border_top_color:
		v.Color = s.BorderColor.Top
	case p_margin_right:
		v.Length = s.Margin.Right
	case p_padding_right:
		v.Length = s.Padding.Right
	case p_border_right_width:
		v.Length = s.BorderWidth.Right
	case p_border_right_style:
		v.Keyword = keywordNames[s.BorderStyle.Right]
	case p_border_right_color:
		v.Color = s.BorderColor.Right
	case p_margin_bottom:
		v.Length = s.Margin.Bottom
	case p_padding_bottom:
		v.Length = s.Padding.Bottom
	case p_border_bottom_width:
		v.Length = s.BorderWidth.Bottom
	case p_border_bottom_style:
		v.Keyword = keywordNames[s.BorderStyle.Bottom]
	case p_border_bottom_color:
		v.Color = s.BorderColor.Bottom
	case p_margin_left:
		v.Length = s.Margin.Left
	case p_padding_left:
		v.Length = s.Padding.Left
	case p_border_left_width:
		v.Length = s.BorderWidth.Left
	case p_border_left_style:
		v.Keyword = keywordNames[s.BorderStyle.Left]
	case p_border_left_color:
		v.Color = s.BorderColor.Left
	case p_border_top_left_radius:
		v.Length = s.BorderRadius.TopLeft
	case p_border_top_right_radius:
		v.Length = s.BorderRadius.TopRight
	case p_border_bottom_right_radius:
		v.Length = s.BorderRadius.BottomRight
	case p_border_bottom_left_radius:
		v.Length = s.BorderRadius.BottomLeft
	case p_accent_color:
		v.Color = s.AccentColor
	}
	return v
}
