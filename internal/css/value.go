package css

import (
	"bytes"
	"encoding"
	"errors"
	"fmt"
	"io"
	"iter"
	"slices"
)

var (
	_ encoding.TextMarshaler   = Value(nil)
	_ encoding.TextAppender    = Value(nil)
	_ encoding.TextUnmarshaler = (*Value)(nil)
)

// Value is a CSS component value:
// a {}-block, a ()-block, a []-block, a function-block, or a preserved token.
type Value []Token

// SplitValues returns an iterator over the component values in the slice of tokens.
func SplitValues(tokens []Token) iter.Seq[Value] {
	return func(yield func(Value) bool) {
		for tokens := tokens; len(tokens) > 0; {
			var v Value
			v, tokens, _ = cutValue(tokens)
			if !yield(v) {
				return
			}
		}
	}
}

// SplitCommaSeparatedValues returns an iterator over all subslices of tokens
// separated by comma [DelimKind] tokens.
func SplitCommaSeparatedValues(tokens []Token) iter.Seq[[]Token] {
	return func(yield func([]Token) bool) {
		i, j := 0, 0
		for {
			v, _, _ := cutValue(tokens[j:])
			if v.Kind() == CommaKind {
				if !yield(tokens[i:j]) {
					return
				}
				j += len(v)
				i = j
				continue
			}
			if len(v) == 0 {
				if !yield(tokens[i:]) {
					return
				}
				break
			}
			j += len(v)
		}
	}
}

// Kind returns the [Kind] of the first [Token] in v.
// If len(v) == 0, then Kind returns [EOFKind].
func (v Value) Kind() Kind {
	if len(v) == 0 {
		return EOFKind
	}
	return v[0].Kind
}

// Tokens returns an iterator over the tokens that this value represents.
func (v Value) Tokens() iter.Seq[Token] {
	return slices.Values(v)
}

// IsValid reports whether v holds a single valid CSS component value.
func (v Value) IsValid() bool {
	switch v.Kind() {
	case EOFKind, WhitespaceKind, RParenKind, RBracketKind, RBraceKind:
		return false
	case LParenKind, LBracketKind, LBraceKind:
		_, ok := v.BlockContents()
		return ok
	default:
		return len(v) == 1 && isKnownKind(v[0].Kind)
	}
}

// BlockContents returns a slice of the tokens inside of a block value.
// ok is true if and only if v represents a block,
// all block-introducing tokens are matched,
// and v does not contain any trailing tokens.
func (v Value) BlockContents() (contents []Token, ok bool) {
	block, tail, ok := cutValue(v)
	if !ok || len(block) < 2 || len(tail) > 0 {
		return nil, false
	}
	return block[1 : len(block)-1], true
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
	if tok, err := p.stream.Next(); tok.Kind != EOFKind {
		return fmt.Errorf("parse css value: unexpected %v", tok)
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("parse css value: %w", err)
	}
	return nil
}

// cutValue finds the end of the component value
// that starts at the beginning of the slice of tokens.
// ok is true if and only if tokens starts with a preserved token
// or a properly closed block.
// If tokens starts with an unclosed block,
// then cutValue returns (tokens, tokens[len(tokens):], false).
func cutValue(tokens []Token) (head Value, tail []Token, ok bool) {
	if len(tokens) == 0 {
		return nil, nil, false
	}
	_, end, ok := blockKind(tokens[0].Kind)
	if !ok {
		return tokens[:1], tokens[1:], true
	}
	stack := []Kind{end}
	i := 1
	for ; len(stack) > 0 && i < len(tokens); i++ {
		// CSS parsing only considers the top of the stack.
		// https://www.w3.org/TR/css-syntax-3/#consume-a-simple-block
		if tokens[i].Kind == stack[len(stack)-1] {
			stack = stack[:len(stack)-1]
		} else if _, end, ok := blockKind(tokens[i].Kind); ok {
			stack = append(stack, end)
		}
	}
	return tokens[:i], tokens[i:], len(stack) == 0
}
