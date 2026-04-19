package css

import (
	"bytes"
	"encoding"
	"errors"
	"fmt"
	"io"
	"iter"
)

var (
	_ encoding.TextMarshaler   = Value(nil)
	_ encoding.TextAppender    = Value(nil)
	_ encoding.TextUnmarshaler = (*Value)(nil)
)

// Value is a CSS component value:
// a {}-block, a ()-block, a []-block, a function-block, or a preserved token.
type Value []Token

// Kind returns the [Kind] of the first [Token] in v.
// If len(v) == 0, then Kind returns [EOFKind].
func (v Value) Kind() Kind {
	if len(v) == 0 {
		return EOFKind
	}
	return v[0].Kind
}

// IsValid reports whether v holds a single valid CSS component value.
func (v Value) IsValid() bool {
	switch v.Kind() {
	case EOFKind, WhitespaceKind, RParenKind, RBracketKind, RBraceKind:
		return false
	case LParenKind, LBracketKind, LBraceKind:
		_, ok := v.BlockBody()
		return ok
	default:
		return len(v) == 1 && isKnownKind(v[0].Kind)
	}
}

// BlockBody returns a slice of the tokens inside of a block value.
// ok is true if and only if v represents a block,
// all block-introducing tokens are matched,
// and v does not contain any trailing tokens.
func (v Value) BlockBody() (body []Token, ok bool) {
	if len(v) < 2 {
		return nil, false
	}
	_, end, ok := blockKind(v[0].Kind)
	if !ok {
		return nil, false
	}
	stack := []Kind{end}
	i := 1
	for ; len(stack) > 0 && i < len(v); i++ {
		if v[i].Kind == stack[len(stack)-1] {
			stack = stack[:len(stack)-1]
		} else if _, end, ok := blockKind(v[0].Kind); ok {
			stack = append(stack, end)
		}
	}
	if len(stack) > 0 || i < len(v) {
		return nil, false
	}
	return v[1 : len(v)-1], true
}

// MarshalText implements [encoding.TextMarshaler] by serializing the tokens.
func (v Value) MarshalText() ([]byte, error) {
	return v.AppendText(nil)
}

// AppendText implements [encoding.TextAppender] by appending the tokens to dst.
func (v Value) AppendText(dst []byte) ([]byte, error) {
	buf := bytes.NewBuffer(dst)
	w := NewWriter(buf)
	for _, tok := range v {
		if err := w.WriteToken(tok); err != nil {
			return buf.Bytes(), err
		}
	}
	return buf.Bytes(), nil
}

// UnmarshalText implements [encoding.TextUnmarshaler]
// by parsing the text as a single CSS component value.
func (v *Value) UnmarshalText(text []byte) error {
	p := NewParser(NewScanner(bytes.NewReader(text)))
	p.whitespace()
	var err error
	*v, err = p.value((*v)[:0])
	if err != nil {
		return err
	}
	p.whitespace()
	if tok, err := p.next(); tok.Kind != EOFKind {
		return fmt.Errorf("parse css value: unexpected %v", tok)
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("parse css value: %w", err)
	}
	return nil
}

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
		if _, ok := rule.Block.BlockBody(); !ok {
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
	if tok, err := p.next(); tok.Kind != EOFKind {
		return fmt.Errorf("parse css rule: unexpected %v", tok)
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("parse css rule: %w", err)
	}
	return nil
}
