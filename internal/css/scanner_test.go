package css

import (
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func TestScanner(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   []Token
	}{
		{
			name:   "Empty",
			source: "",
			want:   []Token{},
		},
		{
			name: "Example1",
			source: "" +
				"p > a {\n" +
				"\tcolor: blue;\n" +
				"\ttext-decoration: underline;\n" +
				"}\n",
			want: []Token{
				{Kind: IdentKind, Value: "p"},
				{Kind: WhitespaceKind},
				{Kind: DelimKind, Value: ">"},
				{Kind: WhitespaceKind},
				{Kind: IdentKind, Value: "a"},
				{Kind: WhitespaceKind},
				{Kind: LBraceKind},
				{Kind: WhitespaceKind},
				{Kind: IdentKind, Value: "color"},
				{Kind: ColonKind},
				{Kind: WhitespaceKind},
				{Kind: IdentKind, Value: "blue"},
				{Kind: SemicolonKind},
				{Kind: WhitespaceKind},
				{Kind: IdentKind, Value: "text-decoration"},
				{Kind: ColonKind},
				{Kind: WhitespaceKind},
				{Kind: IdentKind, Value: "underline"},
				{Kind: SemicolonKind},
				{Kind: WhitespaceKind},
				{Kind: RBraceKind},
				{Kind: WhitespaceKind},
			},
		},
		{
			name:   "Import",
			source: `@import "my-styles.css";`,
			want: []Token{
				{Kind: AtKeywordKind, Value: "import"},
				{Kind: WhitespaceKind},
				{Kind: StringKind, Value: "my-styles.css"},
				{Kind: SemicolonKind},
			},
		},
		{
			name:   "ImportURL",
			source: `@import url(http://example.com/foo.css);`,
			want: []Token{
				{Kind: AtKeywordKind, Value: "import"},
				{Kind: WhitespaceKind},
				{Kind: URLKind, Value: "http://example.com/foo.css"},
				{Kind: SemicolonKind},
			},
		},
		{
			name: "PageRule",
			source: "" +
				"@page :left {\n" +
				"\tmargin-left: 4cm;\n" +
				"\tmargin-right: 3cm;\n" +
				"}\n",
			want: []Token{
				{Kind: AtKeywordKind, Value: "page"},
				{Kind: WhitespaceKind},
				{Kind: ColonKind},
				{Kind: IdentKind, Value: "left"},
				{Kind: WhitespaceKind},
				{Kind: LBraceKind},
				{Kind: WhitespaceKind},
				{Kind: IdentKind, Value: "margin-left"},
				{Kind: ColonKind},
				{Kind: WhitespaceKind},
				{Kind: DimensionKind, Value: "4", Unit: "cm"},
				{Kind: SemicolonKind},
				{Kind: WhitespaceKind},
				{Kind: IdentKind, Value: "margin-right"},
				{Kind: ColonKind},
				{Kind: WhitespaceKind},
				{Kind: DimensionKind, Value: "3", Unit: "cm"},
				{Kind: SemicolonKind},
				{Kind: WhitespaceKind},
				{Kind: RBraceKind},
				{Kind: WhitespaceKind},
			},
		},
		{
			name: "MediaRule",
			source: "" +
				"@media print {\n" +
				"\tbody { font-size: 10pt }\n" +
				"}\n",
			want: []Token{
				{Kind: AtKeywordKind, Value: "media"},
				{Kind: WhitespaceKind},
				{Kind: IdentKind, Value: "print"},
				{Kind: WhitespaceKind},
				{Kind: LBraceKind},
				{Kind: WhitespaceKind},
				{Kind: IdentKind, Value: "body"},
				{Kind: WhitespaceKind},
				{Kind: LBraceKind},
				{Kind: WhitespaceKind},
				{Kind: IdentKind, Value: "font-size"},
				{Kind: ColonKind},
				{Kind: WhitespaceKind},
				{Kind: DimensionKind, Value: "10", Unit: "pt"},
				{Kind: WhitespaceKind},
				{Kind: RBraceKind},
				{Kind: WhitespaceKind},
				{Kind: RBraceKind},
				{Kind: WhitespaceKind},
			},
		},
		{
			name:   "ShortEscape",
			source: `\26 B`,
			want: []Token{
				{Kind: IdentKind, Value: "&B"},
			},
		},
		{
			name:   "LongEscape",
			source: `\000026B`,
			want: []Token{
				{Kind: IdentKind, Value: "&B"},
			},
		},
		{
			name:   "Function",
			source: `background-color:rgb(255, 0, 0)`,
			want: []Token{
				{Kind: IdentKind, Value: "background-color"},
				{Kind: ColonKind},
				{Kind: FunctionKind, Value: "rgb"},
				{Kind: NumberKind, Value: "255"},
				{Kind: CommaKind},
				{Kind: WhitespaceKind},
				{Kind: NumberKind, Value: "0"},
				{Kind: CommaKind},
				{Kind: WhitespaceKind},
				{Kind: NumberKind, Value: "0"},
				{Kind: RParenKind},
			},
		},
		{
			name:   "HexCode",
			source: `background-color:#ff0000`,
			want: []Token{
				{Kind: IdentKind, Value: "background-color"},
				{Kind: ColonKind},
				{Kind: HashKind, Value: "ff0000"},
			},
		},
		{
			name:   "AttributeSelector",
			source: `a[class~="logo"]`,
			want: []Token{
				{Kind: IdentKind, Value: "a"},
				{Kind: LBracketKind},
				{Kind: IdentKind, Value: "class"},
				{Kind: DelimKind, Value: "~"},
				{Kind: DelimKind, Value: "="},
				{Kind: StringKind, Value: "logo"},
				{Kind: RBracketKind},
			},
		},
		{
			name:   "Calc",
			source: `width: calc(3 * (100% - 20px))`,
			want: []Token{
				{Kind: IdentKind, Value: "width"},
				{Kind: ColonKind},
				{Kind: WhitespaceKind},
				{Kind: FunctionKind, Value: "calc"},
				{Kind: NumberKind, Value: "3"},
				{Kind: WhitespaceKind},
				{Kind: DelimKind, Value: "*"},
				{Kind: WhitespaceKind},
				{Kind: LParenKind},
				{Kind: PercentageKind, Value: "100"},
				{Kind: WhitespaceKind},
				{Kind: DelimKind, Value: "-"},
				{Kind: WhitespaceKind},
				{Kind: DimensionKind, Value: "20", Unit: "px"},
				{Kind: RParenKind},
				{Kind: RParenKind},
			},
		},
		{
			name:   "CalcVar",
			source: `width: calc(var(--variable-width) + 20px);`,
			want: []Token{
				{Kind: IdentKind, Value: "width"},
				{Kind: ColonKind},
				{Kind: WhitespaceKind},
				{Kind: FunctionKind, Value: "calc"},
				{Kind: FunctionKind, Value: "var"},
				{Kind: IdentKind, Value: "--variable-width"},
				{Kind: RParenKind},
				{Kind: WhitespaceKind},
				{Kind: DelimKind, Value: "+"},
				{Kind: WhitespaceKind},
				{Kind: DimensionKind, Value: "20", Unit: "px"},
				{Kind: RParenKind},
				{Kind: SemicolonKind},
			},
		},
		{
			name:   "Class",
			source: `.foo { color: red }`,
			want: []Token{
				{Kind: DelimKind, Value: "."},
				{Kind: IdentKind, Value: "foo"},
				{Kind: WhitespaceKind},
				{Kind: LBraceKind},
				{Kind: WhitespaceKind},
				{Kind: IdentKind, Value: "color"},
				{Kind: ColonKind},
				{Kind: WhitespaceKind},
				{Kind: IdentKind, Value: "red"},
				{Kind: WhitespaceKind},
				{Kind: RBraceKind},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := NewScanner(strings.NewReader(test.source))
			var got []Token
			for {
				tok, err := s.Next()
				if tok.Kind == EOFKind {
					if want := (Token{}); !cmp.Equal(tok, want) {
						t.Errorf("final token = %+v; want %+v", tok, want)
					}
					if err != io.EOF {
						t.Error("Error:", err)
					}
					break
				}
				if err != nil {
					t.Errorf("Error on token %d: %v", len(got), err)
				}
				got = append(got, tok)
			}

			if diff := cmp.Diff(test.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("-want +got:\n%s", diff)
			}
		})
	}
}

func TestBufferedReader(t *testing.T) {
	tests := []struct {
		s         string
		rewritten string
	}{
		{"", ""},
		{"foo", "foo"},
		{"foo\nbar", "foo\nbar"},
		{"foo\r\nbar", "foo\nbar"},
		{"foo\rbar", "foo\nbar"},
		{"foo\r", "foo\n"},
		{"foo\fbar", "foo\nbar"},
		{"foo\x00bar", "foo\ufffdbar"},
	}

	t.Run("ReadRune", func(t *testing.T) {
		for _, test := range tests {
			br := newBufferedReader(strings.NewReader(test.s))
			var got []rune
			for {
				r, _, err := br.ReadRune()
				if err != nil {
					if err != io.EOF {
						t.Errorf("after %d runes from %q, got error: %v", len(got), test.s, err)
					}
					break
				}
				got = append(got, r)
			}
			if !slices.Equal(got, []rune(test.rewritten)) {
				t.Errorf("%q read as %q; want %q", test.s, string(got), test.rewritten)
			}
		}
	})

	t.Run("Peek", func(t *testing.T) {
		for _, test := range tests {
			const peekStep = scannerLookAhead - 1

			br := newBufferedReader(strings.NewReader(test.s))
			var got []rune
			for {
				wantPeek := []rune(test.rewritten)[len(got):]
				var wantPeekError error
				if len(wantPeek) < peekStep {
					wantPeekError = io.EOF
				} else {
					wantPeek = wantPeek[:peekStep]
				}

				gotPeek, err := br.peek(peekStep)
				if !slices.Equal(gotPeek, wantPeek) || err != wantPeekError {
					t.Errorf("after %d runes from %q, br.peek(%d) = %q, %v; want %q, %v",
						len(got), test.s, peekStep, string(gotPeek), err, string(wantPeek), wantPeekError)
				}

				r, _, err := br.ReadRune()
				if err != nil {
					if err != io.EOF {
						t.Errorf("after %d runes from %q, got error: %v", len(got), test.s, err)
					}
					break
				}
				got = append(got, r)
			}
			if !slices.Equal(got, []rune(test.rewritten)) {
				t.Errorf("%q read as %q; want %q", test.s, string(got), test.rewritten)
			}
		}
	})
}
