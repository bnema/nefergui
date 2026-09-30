package css

import "testing"

func TestCommentsSeparateTokens(t *testing.T) {
	if got := Parse("button{color:r/*c*/ed; color:blue}"); got.Rules[0].Declarations[0].Value != "r ed" || Compile(Sheet{}, got).Compute(&Element{Type: "button"}, nil).Style.Color != namedColors["blue"] {
		t.Fatalf("comment joined value: %+v", got)
	}
	if got := Parse("bu/*c*/tton{color:red} button{color:blue}"); Compile(Sheet{}, got).Compute(&Element{Type: "button"}, nil).Style.Color != namedColors["blue"] {
		t.Fatalf("comment joined selector: %+v", got)
	}
}
