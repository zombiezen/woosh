// Copyright 2026 Roxy Light
// SPDX-License-Identifier: MIT

package css

import (
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func TestWriter(t *testing.T) {
	tests := []struct {
		name   string
		tokens []Token
		want   string
	}{
		{
			name:   "Empty",
			tokens: []Token{},
			want:   "",
		},
		{
			name: "AfterFirstBrace",
			tokens: []Token{
				{Kind: IdentKind, Value: "p"},
				{Kind: WhitespaceKind},
				{Kind: DelimKind, Value: ">"},
				{Kind: WhitespaceKind},
				{Kind: IdentKind, Value: "a"},
				{Kind: WhitespaceKind},
				{Kind: LBraceKind},
			},
			want: "p > a {",
		},
		{
			name: "WhitespaceAfterFirstBrace",
			tokens: []Token{
				{Kind: IdentKind, Value: "p"},
				{Kind: WhitespaceKind},
				{Kind: DelimKind, Value: ">"},
				{Kind: WhitespaceKind},
				{Kind: IdentKind, Value: "a"},
				{Kind: WhitespaceKind},
				{Kind: LBraceKind},
				{Kind: WhitespaceKind},
			},
			want: "p > a {\n",
		},
		{
			name: "FullRule",
			tokens: []Token{
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
				{Kind: RBraceKind},
				{Kind: WhitespaceKind},
			},
			want: "" +
				"p > a {\n" +
				"\tcolor: blue;\n" +
				"}\n",
		},
		{
			name: "Hash",
			tokens: []Token{
				{Kind: IdentKind, Value: "color"},
				{Kind: ColonKind},
				{Kind: WhitespaceKind},
				{Kind: HashKind, Value: "000"},
			},
			want: "color: #000",
		},
		{
			name: "FancyIdent",
			tokens: []Token{
				{Kind: IdentKind, Value: "000"},
			},
			want: `\30 00`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sb := new(strings.Builder)
			w := NewWriter(sb)
			for _, tok := range test.tokens {
				if err := w.WriteToken(tok); err != nil {
					t.Error(err)
				}
			}
			if diff := cmp.Diff(test.want, sb.String()); diff != "" {
				t.Errorf("output (-want +got):\n%s", diff)
			}
		})
	}
}

// FuzzWriter verifies that tokens can be round-tripped through the scanner.
func FuzzWriter(f *testing.F) {
	for _, test := range scannerTests {
		f.Add(test.source)
	}

	f.Fuzz(func(t *testing.T, css string) {
		s := NewScanner(strings.NewReader(css))
		var tokens1 []Token
		for {
			tok, err := s.Next()
			if tok.Kind == EOFKind {
				if err != io.EOF {
					t.Skip("At EOF:", err)
				}
				break
			}
			if err != nil {
				t.Skipf("Error on token %d: %v", len(tokens1), err)
			}
			tokens1 = append(tokens1, tok)
		}

		buf := new(bytes.Buffer)
		w := NewWriter(buf)
		for i, tok := range tokens1 {
			if err := w.WriteToken(tok); err != nil {
				t.Fatalf("Write tokens[%d] (token=%v): %v", i, tok, err)
			}
		}
		t.Logf("Original:\n%s", css)
		t.Logf("Rewritten:\n%s", buf)

		s = NewScanner(buf)
		var tokens2 []Token
		for {
			tok, err := s.Next()
			if tok.Kind == EOFKind {
				if err != io.EOF {
					t.Error("At EOF:", err)
				}
				break
			}
			if err != nil {
				t.Errorf("Error on token %d: %v", len(tokens2), err)
			}
			tokens2 = append(tokens2, tok)
		}
		ignoreLocation := cmp.FilterPath(func(p cmp.Path) bool {
			return p.Index(-2).Type() == reflect.TypeFor[Token]() &&
				p.Last().(cmp.StructField).Name() == "Start"
		}, cmp.Ignore())
		if diff := cmp.Diff(tokens1, tokens2, cmpopts.EquateEmpty(), ignoreLocation); diff != "" {
			t.Errorf("-want +got:\n%s", diff)
		}
	})
}
