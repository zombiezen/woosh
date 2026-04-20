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
	if tok, err := p.stream.Next(); tok.Kind != EOFKind {
		return fmt.Errorf("parse css rule: unexpected %v", tok)
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("parse css rule: %w", err)
	}
	return nil
}
