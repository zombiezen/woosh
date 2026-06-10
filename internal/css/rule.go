// Copyright 2026 Roxy Light
// SPDX-License-Identifier: MIT

package css

import (
	"bytes"
	"encoding"
	"errors"
	"fmt"
	"io"
	"iter"
	"strings"
)

var (
	_ encoding.TextMarshaler   = (*Rule)(nil)
	_ encoding.TextAppender    = (*Rule)(nil)
	_ encoding.TextUnmarshaler = (*Rule)(nil)
)

// A Rule is a set of tokens followed by a {}-block.
// It may optionally start with an at-keyword.
type Rule struct {
	AtRule     string
	AtLocation Location
	Prelude    []Token
	Block      Value
}

// Start returns the [Location] of the first token in the rule.
func (rule *Rule) Start() Location {
	if rule.AtRule != "" {
		return rule.AtLocation
	}
	if len(rule.Prelude) > 0 {
		return rule.Prelude[0].Start
	}
	if len(rule.Block) > 0 {
		return rule.Block[0].Start
	}
	return Location{}
}

// AtKeyword returns the rule's at-keyword token, if present.
func (rule *Rule) AtKeyword() (Token, bool) {
	if rule == nil || rule.AtRule == "" {
		return Token{}, false
	}
	return Token{
		Kind:  AtKeywordKind,
		Value: rule.AtRule,
		Start: rule.AtLocation,
	}, true
}

// Tokens returns an iterator over the tokens that this rule represents.
func (rule *Rule) Tokens() iter.Seq[Token] {
	if rule == nil {
		return func(yield func(Token) bool) {}
	}
	return func(yield func(Token) bool) {
		atKeyword, isAtRule := rule.AtKeyword()
		if isAtRule {
			if !yield(atKeyword) {
				return
			}
		}
		for _, tok := range rule.Prelude {
			if !yield(tok) {
				return
			}
		}
		for _, tok := range rule.Block {
			if !yield(tok) {
				return
			}
		}
		if len(rule.Block) == 0 && isAtRule {
			if !yield(Token{Kind: SemicolonKind}) {
				return
			}
		}
	}
}

// BlockContents returns an iterator over the declarations and rules in this rule's block.
func (rule *Rule) BlockContents() iter.Seq[BlockPart] {
	if rule == nil {
		return func(yield func(BlockPart) bool) {}
	}
	return func(yield func(BlockPart) bool) {
		contents, ok := rule.Block.BlockContents()
		if !ok {
			return
		}
		SplitBlockContents(contents)(yield)
	}
}

// MarshalText implements [encoding.TextMarshaler] by serializing the rule's tokens.
func (rule *Rule) MarshalText() ([]byte, error) {
	return rule.AppendText(nil)
}

// AppendText implements [encoding.TextAppender] by appending the rule's tokens to dst.
func (rule *Rule) AppendText(dst []byte) ([]byte, error) {
	if len(rule.Block) > 0 {
		if rule.Block[0].Kind != LBraceKind {
			return dst, fmt.Errorf("marshal css rule: {}-block starts with %v instead of {", rule.Block[0])
		}
		if _, ok := rule.Block.BlockContents(); !ok {
			return dst, fmt.Errorf("marshal css rule: {}-block is not valid")
		}
	}

	buf := bytes.NewBuffer(dst)
	w := NewWriter(buf)
	for tok := range rule.Tokens() {
		if err := w.WriteToken(tok); err != nil {
			return buf.Bytes(), err
		}
	}
	return buf.Bytes(), nil
}

// UnmarshalText implements [encoding.TextUnmarshaler]
// by parsing the text as a single CSS rule.
func (rule *Rule) UnmarshalText(text []byte) error {
	p := NewParser(NewScanner(bytes.NewReader(text)))
	rule0, err := p.NextRule()
	if err != nil {
		return err
	}
	*rule = *rule0
	p.whitespace()
	if tok, err := p.stream.Next(); tok.Kind != EOFKind {
		return fmt.Errorf("parse css rule: unexpected %v", tok)
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("parse css rule: %w", err)
	}
	return nil
}

// A Declaration is a name/value pair with an optional !important flag.
type Declaration struct {
	Name      string
	NameStart Location
	Value     []Token
	Important bool
}

// IsCustomProperty reports whether the declaration is a [custom property].
//
// [custom property]: https://drafts.csswg.org/css-variables-2/#custom-property
func (decl *Declaration) IsCustomProperty() bool {
	return decl != nil && isCustomPropertyName(decl.Name)
}

// Tokens returns an iterator over the tokens that this declaration represents.
func (decl *Declaration) Tokens() iter.Seq[Token] {
	return func(yield func(Token) bool) {
		if !yield(Token{Kind: IdentKind, Value: decl.Name, Start: decl.NameStart}) {
			return
		}
		if !yield(Token{Kind: ColonKind}) {
			return
		}
		if !yield(Token{Kind: WhitespaceKind}) {
			return
		}
		for _, tok := range decl.Value {
			if !yield(tok) {
				return
			}
		}
		if decl.Important {
			if !yield(Token{Kind: WhitespaceKind}) {
				return
			}
			if !yield(Token{Kind: DelimKind, Value: "!"}) {
				return
			}
			if !yield(Token{Kind: IdentKind, Value: "important"}) {
				return
			}
		}
		if !yield(Token{Kind: SemicolonKind}) {
			return
		}
	}
}

func isCustomPropertyName(name string) bool {
	const prefix = "--"
	return len(name) > len(prefix) && strings.HasPrefix(name, prefix)
}

// A BlockPart is a [*Declaration] or a [*Rule].
// The zero value is a nil pointer.
type BlockPart struct {
	x any
}

// SplitBlockContents returns an iterator over the declarations and rules
// from a list of tokens.
// The list of tokens should not include the enclosing braces.
func SplitBlockContents(tokens []Token) iter.Seq[BlockPart] {
	return func(yield func(BlockPart) bool) {
		p := ParseTokens(tokens)
		for {
			part, _ := p.blockPart()
			if part.IsZero() {
				break
			}
			if !yield(part) {
				return
			}
		}
	}
}

// ToBlockPart converts a pointer to a [BlockPart].
// If x is a nil pointer, then ToBlockPart returns a zero [BlockPart].
func ToBlockPart[T *Declaration | *Rule](x T) BlockPart {
	if x == nil {
		return BlockPart{}
	}
	return BlockPart{x}
}

// IsZero reports whether part is the zero value.
func (part BlockPart) IsZero() bool {
	return part.x == nil
}

// Declaration converts the [BlockPart] back to a [*Declaration].
// If the [BlockPart] is not a [*Declaration], then Declaration returns nil.
func (part BlockPart) Declaration() *Declaration {
	decl, _ := part.x.(*Declaration)
	return decl
}

// Rule converts the [BlockPart] back to a [*Rule].
// If the [BlockPart] is not a [*Rule], then Rule returns nil.
func (part BlockPart) Rule() *Rule {
	r, _ := part.x.(*Rule)
	return r
}

// FirstToken returns the first [Token] of the [BlockPart].
func (part BlockPart) FirstToken() (_ Token, ok bool) {
	switch x := part.x.(type) {
	case *Rule:
		nextToken, stop := iter.Pull(x.Tokens())
		tok, ok := nextToken()
		stop()
		return tok, ok
	case *Declaration:
		return Token{
			Kind:  IdentKind,
			Value: x.Name,
			Start: x.NameStart,
		}, true
	default:
		return Token{}, false
	}
}
