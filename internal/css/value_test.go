// Copyright 2026 Roxy Light
// SPDX-License-Identifier: MIT

package css

import (
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func TestSplitCommaSeparatedValues(t *testing.T) {
	tests := []struct {
		css  string
		want [][]Token
	}{
		{css: "", want: [][]Token{{}}},
		{css: "foo", want: [][]Token{
			{
				{Kind: IdentKind, Value: "foo", Start: Location{Offset: 0, Line: 1}},
			},
		}},
		{css: "foo,", want: [][]Token{
			{
				{Kind: IdentKind, Value: "foo", Start: Location{Offset: 0, Line: 1}},
			},
			{},
		}},
		{css: "foo,bar", want: [][]Token{
			{
				{Kind: IdentKind, Value: "foo", Start: Location{Offset: 0, Line: 1}},
			},
			{
				{Kind: IdentKind, Value: "bar", Start: Location{Offset: 4, Line: 1}},
			},
		}},
		{css: "foo, bar", want: [][]Token{
			{
				{Kind: IdentKind, Value: "foo", Start: Location{Offset: 0, Line: 1}},
			},
			{
				{Kind: WhitespaceKind, Start: Location{Offset: 4, Line: 1}},
				{Kind: IdentKind, Value: "bar", Start: Location{Offset: 5, Line: 1}},
			},
		}},
		{css: "rgb(255, 0, 0), bar", want: [][]Token{
			{
				{Kind: FunctionKind, Value: "rgb", Start: Location{Offset: 0, Line: 1}},
				{Kind: NumberKind, Value: "255", Start: Location{Offset: 4, Line: 1}},
				{Kind: CommaKind, Start: Location{Offset: 7, Line: 1}},
				{Kind: WhitespaceKind, Start: Location{Offset: 8, Line: 1}},
				{Kind: NumberKind, Value: "0", Start: Location{Offset: 9, Line: 1}},
				{Kind: CommaKind, Start: Location{Offset: 10, Line: 1}},
				{Kind: WhitespaceKind, Start: Location{Offset: 11, Line: 1}},
				{Kind: NumberKind, Value: "0", Start: Location{Offset: 12, Line: 1}},
				{Kind: RParenKind, Start: Location{Offset: 13, Line: 1}},
			},
			{
				{Kind: WhitespaceKind, Start: Location{Offset: 15, Line: 1}},
				{Kind: IdentKind, Value: "bar", Start: Location{Offset: 16, Line: 1}},
			},
		}},
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

		got := slices.Collect(SplitCommaSeparatedValues(tokens))
		if diff := cmp.Diff(test.want, got, cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("SplitCommaSeparatedValues(%q) (-want +got):\n%s", test.css, diff)
		}
	}
}

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
