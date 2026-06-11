// Copyright 2026 Roxy Light
// SPDX-License-Identifier: MIT

package css

import (
	"io"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

var scannerTests = []struct {
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
			{Kind: IdentKind, Value: "p", Start: Location{Line: 1, Offset: 0}},
			{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 1}},
			{Kind: DelimKind, Value: ">", Start: Location{Line: 1, Offset: 2}},
			{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 3}},
			{Kind: IdentKind, Value: "a", Start: Location{Line: 1, Offset: 4}},
			{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 5}},
			{Kind: LBraceKind, Start: Location{Line: 1, Offset: 6}},
			{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 7}},
			{Kind: IdentKind, Value: "color", Start: Location{Line: 2, Offset: 9}},
			{Kind: ColonKind, Start: Location{Line: 2, Offset: 14}},
			{Kind: WhitespaceKind, Start: Location{Line: 2, Offset: 15}},
			{Kind: IdentKind, Value: "blue", Start: Location{Line: 2, Offset: 16}},
			{Kind: SemicolonKind, Start: Location{Line: 2, Offset: 20}},
			{Kind: WhitespaceKind, Start: Location{Line: 2, Offset: 21}},
			{Kind: IdentKind, Value: "text-decoration", Start: Location{Line: 3, Offset: 23}},
			{Kind: ColonKind, Start: Location{Line: 3, Offset: 38}},
			{Kind: WhitespaceKind, Start: Location{Line: 3, Offset: 39}},
			{Kind: IdentKind, Value: "underline", Start: Location{Line: 3, Offset: 40}},
			{Kind: SemicolonKind, Start: Location{Line: 3, Offset: 49}},
			{Kind: WhitespaceKind, Start: Location{Line: 3, Offset: 50}},
			{Kind: RBraceKind, Start: Location{Line: 4, Offset: 51}},
			{Kind: WhitespaceKind, Start: Location{Line: 4, Offset: 52}},
		},
	},
	{
		name:   "Import",
		source: `@import "my-styles.css";`,
		want: []Token{
			{Kind: AtKeywordKind, Value: "import", Start: Location{Line: 1, Offset: 0}},
			{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 7}},
			{Kind: StringKind, Value: "my-styles.css", Start: Location{Line: 1, Offset: 8}},
			{Kind: SemicolonKind, Start: Location{Line: 1, Offset: 23}},
		},
	},
	{
		name:   "ImportURL",
		source: `@import url(http://example.com/foo.css);`,
		want: []Token{
			{Kind: AtKeywordKind, Value: "import", Start: Location{Line: 1, Offset: 0}},
			{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 7}},
			{Kind: URLKind, Value: "http://example.com/foo.css", Start: Location{Line: 1, Offset: 8}},
			{Kind: SemicolonKind, Start: Location{Line: 1, Offset: 39}},
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
			{Kind: AtKeywordKind, Value: "page", Start: Location{Line: 1, Offset: 0}},
			{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 5}},
			{Kind: ColonKind, Start: Location{Line: 1, Offset: 6}},
			{Kind: IdentKind, Value: "left", Start: Location{Line: 1, Offset: 7}},
			{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 11}},
			{Kind: LBraceKind, Start: Location{Line: 1, Offset: 12}},
			{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 13}},
			{Kind: IdentKind, Value: "margin-left", Start: Location{Line: 2, Offset: 15}},
			{Kind: ColonKind, Start: Location{Line: 2, Offset: 26}},
			{Kind: WhitespaceKind, Start: Location{Line: 2, Offset: 27}},
			{Kind: DimensionKind, Value: "4", Unit: "cm", Start: Location{Line: 2, Offset: 28}},
			{Kind: SemicolonKind, Start: Location{Line: 2, Offset: 31}},
			{Kind: WhitespaceKind, Start: Location{Line: 2, Offset: 32}},
			{Kind: IdentKind, Value: "margin-right", Start: Location{Line: 3, Offset: 34}},
			{Kind: ColonKind, Start: Location{Line: 3, Offset: 46}},
			{Kind: WhitespaceKind, Start: Location{Line: 3, Offset: 47}},
			{Kind: DimensionKind, Value: "3", Unit: "cm", Start: Location{Line: 3, Offset: 48}},
			{Kind: SemicolonKind, Start: Location{Line: 3, Offset: 51}},
			{Kind: WhitespaceKind, Start: Location{Line: 3, Offset: 52}},
			{Kind: RBraceKind, Start: Location{Line: 4, Offset: 53}},
			{Kind: WhitespaceKind, Start: Location{Line: 4, Offset: 54}},
		},
	},
	{
		name: "MediaRule",
		source: "" +
			"@media print {\n" +
			"\tbody { font-size: 10pt }\n" +
			"}\n",
		want: []Token{
			{Kind: AtKeywordKind, Value: "media", Start: Location{Line: 1, Offset: 0}},
			{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 6}},
			{Kind: IdentKind, Value: "print", Start: Location{Line: 1, Offset: 7}},
			{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 12}},
			{Kind: LBraceKind, Start: Location{Line: 1, Offset: 13}},
			{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 14}},
			{Kind: IdentKind, Value: "body", Start: Location{Line: 2, Offset: 16}},
			{Kind: WhitespaceKind, Start: Location{Line: 2, Offset: 20}},
			{Kind: LBraceKind, Start: Location{Line: 2, Offset: 21}},
			{Kind: WhitespaceKind, Start: Location{Line: 2, Offset: 22}},
			{Kind: IdentKind, Value: "font-size", Start: Location{Line: 2, Offset: 23}},
			{Kind: ColonKind, Start: Location{Line: 2, Offset: 32}},
			{Kind: WhitespaceKind, Start: Location{Line: 2, Offset: 33}},
			{Kind: DimensionKind, Value: "10", Unit: "pt", Start: Location{Line: 2, Offset: 34}},
			{Kind: WhitespaceKind, Start: Location{Line: 2, Offset: 38}},
			{Kind: RBraceKind, Start: Location{Line: 2, Offset: 39}},
			{Kind: WhitespaceKind, Start: Location{Line: 2, Offset: 40}},
			{Kind: RBraceKind, Start: Location{Line: 3, Offset: 41}},
			{Kind: WhitespaceKind, Start: Location{Line: 3, Offset: 42}},
		},
	},
	{
		name:   "ShortEscape",
		source: `\26 B`,
		want: []Token{
			{Kind: IdentKind, Value: "&B", Start: Location{Line: 1, Offset: 0}},
		},
	},
	{
		name:   "LongEscape",
		source: `\000026B`,
		want: []Token{
			{Kind: IdentKind, Value: "&B", Start: Location{Line: 1, Offset: 0}},
		},
	},
	{
		name:   "Function",
		source: `background-color:rgb(255, 0, 0)`,
		want: []Token{
			{Kind: IdentKind, Value: "background-color", Start: Location{Line: 1, Offset: 0}},
			{Kind: ColonKind, Start: Location{Line: 1, Offset: 16}},
			{Kind: FunctionKind, Value: "rgb", Start: Location{Line: 1, Offset: 17}},
			{Kind: NumberKind, Value: "255", Start: Location{Line: 1, Offset: 21}},
			{Kind: CommaKind, Start: Location{Line: 1, Offset: 24}},
			{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 25}},
			{Kind: NumberKind, Value: "0", Start: Location{Line: 1, Offset: 26}},
			{Kind: CommaKind, Start: Location{Line: 1, Offset: 27}},
			{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 28}},
			{Kind: NumberKind, Value: "0", Start: Location{Line: 1, Offset: 29}},
			{Kind: RParenKind, Start: Location{Line: 1, Offset: 30}},
		},
	},
	{
		name:   "HexCode",
		source: `background-color:#ff0000`,
		want: []Token{
			{Kind: IdentKind, Value: "background-color", Start: Location{Line: 1, Offset: 0}},
			{Kind: ColonKind, Start: Location{Line: 1, Offset: 16}},
			{Kind: HashKind, Value: "ff0000", Start: Location{Line: 1, Offset: 17}},
		},
	},
	{
		name:   "AttributeSelector",
		source: `a[class~="logo"]`,
		want: []Token{
			{Kind: IdentKind, Value: "a", Start: Location{Line: 1, Offset: 0}},
			{Kind: LBracketKind, Start: Location{Line: 1, Offset: 1}},
			{Kind: IdentKind, Value: "class", Start: Location{Line: 1, Offset: 2}},
			{Kind: DelimKind, Value: "~", Start: Location{Line: 1, Offset: 7}},
			{Kind: DelimKind, Value: "=", Start: Location{Line: 1, Offset: 8}},
			{Kind: StringKind, Value: "logo", Start: Location{Line: 1, Offset: 9}},
			{Kind: RBracketKind, Start: Location{Line: 1, Offset: 15}},
		},
	},
	{
		name:   "Calc",
		source: `width: calc(3 * (100% - 20px))`,
		want: []Token{
			{Kind: IdentKind, Value: "width", Start: Location{Line: 1, Offset: 0}},
			{Kind: ColonKind, Start: Location{Line: 1, Offset: 5}},
			{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 6}},
			{Kind: FunctionKind, Value: "calc", Start: Location{Line: 1, Offset: 7}},
			{Kind: NumberKind, Value: "3", Start: Location{Line: 1, Offset: 12}},
			{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 13}},
			{Kind: DelimKind, Value: "*", Start: Location{Line: 1, Offset: 14}},
			{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 15}},
			{Kind: LParenKind, Start: Location{Line: 1, Offset: 16}},
			{Kind: PercentageKind, Value: "100", Start: Location{Line: 1, Offset: 17}},
			{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 21}},
			{Kind: DelimKind, Value: "-", Start: Location{Line: 1, Offset: 22}},
			{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 23}},
			{Kind: DimensionKind, Value: "20", Unit: "px", Start: Location{Line: 1, Offset: 24}},
			{Kind: RParenKind, Start: Location{Line: 1, Offset: 28}},
			{Kind: RParenKind, Start: Location{Line: 1, Offset: 29}},
		},
	},
	{
		name:   "CalcVar",
		source: `width: calc(var(--variable-width) + 20px);`,
		want: []Token{
			{Kind: IdentKind, Value: "width", Start: Location{Line: 1, Offset: 0}},
			{Kind: ColonKind, Start: Location{Line: 1, Offset: 5}},
			{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 6}},
			{Kind: FunctionKind, Value: "calc", Start: Location{Line: 1, Offset: 7}},
			{Kind: FunctionKind, Value: "var", Start: Location{Line: 1, Offset: 12}},
			{Kind: IdentKind, Value: "--variable-width", Start: Location{Line: 1, Offset: 16}},
			{Kind: RParenKind, Start: Location{Line: 1, Offset: 32}},
			{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 33}},
			{Kind: DelimKind, Value: "+", Start: Location{Line: 1, Offset: 34}},
			{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 35}},
			{Kind: DimensionKind, Value: "20", Unit: "px", Start: Location{Line: 1, Offset: 36}},
			{Kind: RParenKind, Start: Location{Line: 1, Offset: 40}},
			{Kind: SemicolonKind, Start: Location{Line: 1, Offset: 41}},
		},
	},
	{
		name:   "Class",
		source: `.foo { color: red }`,
		want: []Token{
			{Kind: DelimKind, Value: ".", Start: Location{Line: 1, Offset: 0}},
			{Kind: IdentKind, Value: "foo", Start: Location{Line: 1, Offset: 1}},
			{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 4}},
			{Kind: LBraceKind, Start: Location{Line: 1, Offset: 5}},
			{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 6}},
			{Kind: IdentKind, Value: "color", Start: Location{Line: 1, Offset: 7}},
			{Kind: ColonKind, Start: Location{Line: 1, Offset: 12}},
			{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 13}},
			{Kind: IdentKind, Value: "red", Start: Location{Line: 1, Offset: 14}},
			{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 17}},
			{Kind: RBraceKind, Start: Location{Line: 1, Offset: 18}},
		},
	},
	{
		name:   "EscapeNewlineInString",
		source: "'foo\\\nbar'",
		want: []Token{
			{Kind: StringKind, Value: "foobar", Start: Location{Line: 1, Offset: 0}},
		},
	},
	{
		name:   "EscapeNewlineCodePointInString",
		source: `'foo\0a bar'`,
		want: []Token{
			{Kind: StringKind, Value: "foo\nbar", Start: Location{Line: 1, Offset: 0}},
		},
	},
}

func TestScanner(t *testing.T) {
	for _, test := range scannerTests {
		t.Run(test.name, func(t *testing.T) {
			wantEnd := Location{Line: 1}
			for i := 0; ; {
				r, size := utf8.DecodeRuneInString(test.source[i:])
				if size == 0 {
					break
				}
				wantEnd = addLocation(wantEnd, []rune{r}, []int{size})
				i += size
			}

			s := NewScanner(strings.NewReader(test.source))
			var got []Token
			for {
				tok, err := s.Next()
				if tok.Kind == EOFKind {
					want := (Token{
						Kind:  EOFKind,
						Start: wantEnd,
					})
					if !cmp.Equal(tok, want) {
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
