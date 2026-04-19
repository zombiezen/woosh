package css

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func TestParserRuleList(t *testing.T) {
	tests := []struct {
		name       string
		stylesheet string
		tokens     []Token
		want       []*Rule
	}{
		{
			name:       "Empty",
			stylesheet: "",
			want:       []*Rule{},
		},
		{
			name: "Example1",
			stylesheet: "" +
				"p > a {\n" +
				"\tcolor: blue;\n" +
				"\ttext-decoration: underline;\n" +
				"}\n",
			want: []*Rule{
				{
					Prelude: []Token{
						{Kind: IdentKind, Value: "p", Start: Location{Line: 1, Offset: 0}},
						{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 1}},
						{Kind: DelimKind, Value: ">", Start: Location{Line: 1, Offset: 2}},
						{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 3}},
						{Kind: IdentKind, Value: "a", Start: Location{Line: 1, Offset: 4}},
						{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 5}},
					},
					Block: []Token{
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
					},
				},
			},
		},
		{
			name:       "Import",
			stylesheet: `@import "my-styles.css";`,
			want: []*Rule{
				{
					AtRule:     "import",
					AtLocation: Location{Line: 1, Offset: 0},
					Prelude: []Token{
						{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 7}},
						{Kind: StringKind, Value: "my-styles.css", Start: Location{Line: 1, Offset: 8}},
					},
				},
			},
		},
		{
			name: "PageRule",
			stylesheet: "" +
				"@page :left {\n" +
				"\tmargin-left: 4cm;\n" +
				"\tmargin-right: 3cm;\n" +
				"}\n",
			want: []*Rule{
				{
					AtRule:     "page",
					AtLocation: Location{Line: 1, Offset: 0},
					Prelude: []Token{
						{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 5}},
						{Kind: ColonKind, Start: Location{Line: 1, Offset: 6}},
						{Kind: IdentKind, Value: "left", Start: Location{Line: 1, Offset: 7}},
						{Kind: WhitespaceKind, Start: Location{Line: 1, Offset: 11}},
					},
					Block: []Token{
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
					},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var parser *Parser
			if len(test.tokens) > 0 {
				parser = ParseTokens(test.tokens)
			} else {
				parser = NewParser(NewScanner(strings.NewReader(test.stylesheet)))
			}
			var got []*Rule
			for {
				rule, err := parser.NextRule()
				if err != nil {
					if rule != nil || !errors.Is(err, io.EOF) {
						t.Error(err)
					}
				}
				if rule == nil {
					break
				}
				got = append(got, rule)
			}
			if diff := cmp.Diff(test.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("rules (-want +got):\n%s", diff)
			}
		})
	}
}
