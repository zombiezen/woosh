package css

import (
	"io"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func TestBlockContents(t *testing.T) {
	tests := []struct {
		css  string
		want []Token
		ok   bool
	}{
		{css: "", ok: false},
		{
			css:  "{}",
			want: []Token{},
			ok:   true,
		},
		{
			css: "{foo}",
			want: []Token{
				{Kind: IdentKind, Value: "foo", Start: Location{Offset: 1, Line: 1}},
			},
			ok: true,
		},
		{
			css: "{foo} ",
			ok:  false,
		},
	}

	for _, test := range tests {
		var tokens []Token
		s := NewScanner(strings.NewReader(test.css))
		for {
			tok, err := s.Next()
			if err != nil && (tok.Kind != EOFKind || err != io.EOF) {
				t.Errorf("Error in %q: %v", test.css, err)
			}
			if tok.Kind == EOFKind {
				break
			}
			tokens = append(tokens, tok)
		}

		got, ok := Value(tokens).BlockContents()
		if diff := cmp.Diff(test.want, got, cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("Value(%q).BlockContents() (-want +got):\n%s", test.css, diff)
		}
		if ok != test.ok {
			t.Errorf("Value(%q).BlockContents() = _, %t; want _, %t", test.css, ok, test.ok)
		}
	}
}
