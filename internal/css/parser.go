package css

import (
	"errors"
	"fmt"
	"io"
	"slices"
)

// A Parser groups chunks of tokens according to the [CSS parsing stage].
//
// [CSS parsing stage]: https://www.w3.org/TR/css-syntax-3/#parsing
type Parser struct {
	stream   tokenStream
	topLevel bool
}

// NewParser returns a new [*Parser] that reads tokens from the given [*Scanner].
func NewParser(s *Scanner) *Parser {
	return &Parser{
		stream:   newBufferedScanner(s),
		topLevel: true,
	}
}

// ParseTokens returns a new [*Parser] that reads tokens from the given slice.
func ParseTokens(tokens []Token) *Parser {
	return &Parser{stream: &tokenSlice{tokens: tokens}}
}

// NextRule parses the next rule from the underlying token source.
//
// This corresponds to the [stylesheet entry point] if the parser was created with [NewParser]
// or the [list of rules entry point] otherwise.
//
// [stylesheet entry point]: https://www.w3.org/TR/css-syntax-3/#parse-stylesheet
// [list of rules entry point]: https://www.w3.org/TR/css-syntax-3/#parse-list-of-rules
func (p *Parser) NextRule() (*Rule, error) {
	var parseError error
	for {
		p.stream.Mark()
		tok, err := p.stream.Next()
		if err != nil {
			parseError = errors.Join(parseError, fmt.Errorf("parse css rule: %w", err))
		}
		switch tok.Kind {
		case EOFKind:
			p.stream.DiscardMark()
			return nil, parseError
		case WhitespaceKind:
			// Ignore.
			p.stream.DiscardMark()
		case CDOKind, CDCKind:
			if p.topLevel {
				// Ignore.
				p.stream.DiscardMark()
			} else {
				p.stream.RestoreMark()
				r, err := p.qualifiedRule()
				parseError = errors.Join(parseError, err)
				if r != nil {
					return r, parseError
				}
			}
		case AtKeywordKind:
			p.stream.DiscardMark()
			r, err := p.atRule(tok.Value, tok.Start)
			parseError = errors.Join(parseError, err)
			if r != nil {
				return r, parseError
			}
		default:
			p.stream.RestoreMark()
			r, err := p.qualifiedRule()
			parseError = errors.Join(parseError, err)
			if r != nil {
				return r, parseError
			}
		}
	}
}

func (p *Parser) atRule(name string, loc Location) (*Rule, error) {
	r := &Rule{
		AtRule:     name,
		AtLocation: loc,
	}

	var parseError error
	for {
		p.stream.Mark()
		tok, err := p.stream.Next()
		if err != nil {
			parseError = errors.Join(parseError, fmt.Errorf("parse css @%s rule: %w", name, err))
		}
		switch tok.Kind {
		case EOFKind, SemicolonKind:
			p.stream.DiscardMark()
			return r, parseError
		case LBraceKind:
			p.stream.RestoreMark()
			var err error
			r.Block, err = p.block(nil)
			if err != nil {
				// TODO(soon): Split apart err and wrap each error individually.
				parseError = errors.Join(parseError, fmt.Errorf("parse css @%s rule: %w", name, err))
			}
			return r, parseError
		default:
			p.stream.RestoreMark()
			var err error
			r.Prelude, err = p.value(r.Prelude)
			if err != nil {
				// TODO(soon): Split apart err and wrap each error individually.
				parseError = errors.Join(parseError, fmt.Errorf("parse css @%s rule: %w", name, err))
			}
		}
	}
}

func (p *Parser) qualifiedRule() (*Rule, error) {
	r := new(Rule)

	var parseError error
	for {
		p.stream.Mark()
		tok, err := p.stream.Next()
		if err != nil {
			parseError = errors.Join(parseError, fmt.Errorf("parse css rule: %w", err))
		}
		switch tok.Kind {
		case EOFKind:
			p.stream.DiscardMark()
			return nil, parseError
		case LBraceKind:
			p.stream.RestoreMark()
			var err error
			r.Block, err = p.block(nil)
			if err != nil {
				// TODO(soon): Split apart err and wrap each error individually.
				parseError = errors.Join(parseError, fmt.Errorf("parse css rule: %w", err))
			}
			return r, parseError
		default:
			p.stream.RestoreMark()
			var err error
			r.Prelude, err = p.value(r.Prelude)
			if err != nil {
				// TODO(soon): Split apart err and wrap each error individually.
				parseError = errors.Join(parseError, fmt.Errorf("parse css rule: %w", err))
			}
		}
	}
}

func (p *Parser) value(dst []Token) ([]Token, error) {
	p.stream.Mark()
	tok, err := p.stream.Next()
	if tok.Kind == EOFKind {
		return dst, err
	}
	switch tok.Kind {
	case LBraceKind, LBracketKind, LParenKind:
		p.stream.RestoreMark()
		return p.block(dst)
	case FunctionKind:
		p.stream.RestoreMark()
		return p.function(dst)
	default:
		p.stream.DiscardMark()
		dst = append(dst, tok)
		if err != nil {
			err = fmt.Errorf("parse css value: %w", err)
		}
		return dst, err
	}
}

func (p *Parser) block(dst []Token) ([]Token, error) {
	p.stream.Mark()
	tok, err := p.stream.Next()
	if tok.Kind == EOFKind {
		p.stream.DiscardMark()
		return dst, err
	}
	name, end, isBlock := blockKind(tok.Kind)
	if !isBlock || tok.Kind == FunctionKind {
		p.stream.RestoreMark()
		return dst, fmt.Errorf("parse css block: expected (/[/{ (found %v)", tok)
	}
	p.stream.DiscardMark()

	if err != nil {
		err = fmt.Errorf("parse css %s-block: %w", name, err)
	}
	parseError := err
	dst = append(dst, tok)

	for {
		p.stream.Mark()
		tok, err := p.stream.Next()
		switch tok.Kind {
		case EOFKind:
			p.stream.DiscardMark()
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			parseError = errors.Join(parseError, fmt.Errorf("parse css %s-block: %w", name, err))
			return dst, parseError
		case end:
			p.stream.DiscardMark()
			dst = append(dst, tok)
			if err != nil {
				parseError = errors.Join(parseError, fmt.Errorf("parse css %s-block: %w", name, err))
			}
			return dst, parseError
		default:
			p.stream.RestoreMark()
			var err error
			dst, err = p.value(dst)
			if err != nil {
				// TODO(soon): Split apart err and wrap each error individually.
				parseError = errors.Join(parseError, fmt.Errorf("parse css %s-block: %w", name, err))
			}
		}
	}
}

func (p *Parser) function(dst []Token) ([]Token, error) {
	p.stream.Mark()
	tok, err := p.stream.Next()
	if tok.Kind == EOFKind {
		p.stream.DiscardMark()
		return dst, err
	}
	if tok.Kind != FunctionKind {
		p.stream.RestoreMark()
		return dst, fmt.Errorf("parse css function call: expected %v (found %v)", FunctionKind, tok)
	}
	p.stream.DiscardMark()

	name := tok.Value
	if err != nil {
		err = fmt.Errorf("parse css %s call: %w", name, err)
	}
	parseError := err
	dst = append(dst, tok)

	for {
		p.stream.Mark()
		tok, err := p.stream.Next()
		switch tok.Kind {
		case EOFKind:
			p.stream.DiscardMark()
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			parseError = errors.Join(parseError, fmt.Errorf("parse css %s call: %w", name, err))
			return dst, parseError
		case RParenKind:
			p.stream.DiscardMark()
			dst = append(dst, tok)
			if err != nil {
				parseError = errors.Join(parseError, fmt.Errorf("parse css %s call: %w", name, err))
			}
			return dst, parseError
		default:
			p.stream.RestoreMark()
			var err error
			dst, err = p.value(dst)
			if err != nil {
				// TODO(soon): Split apart err and wrap each error individually.
				parseError = errors.Join(parseError, fmt.Errorf("parse css %s call: %w", name, err))
			}
		}
	}
}

func (p *Parser) whitespace() {
	for {
		p.stream.Mark()
		tok, err := p.stream.Next()
		if err != nil {
			p.stream.DiscardMark()
			return
		}
		if tok.Kind != WhitespaceKind {
			p.stream.RestoreMark()
			return
		}
		p.stream.DiscardMark()
	}
}

// tokenStream represents a [CSS token stream] that supports arbitrary lookahead.
//
// [CSS token stream]: https://web.archive.org/web/20260414121525/https://drafts.csswg.org/css-syntax/#parser-definitions
type tokenStream interface {
	// Next consumes a token and returns the token
	// and any parse error that occurred.
	Next() (Token, error)
	// Mark pushes the current position in the stream into the marked indexes stack.
	Mark()
	// RestoreMark pops a position from the marked indexes stack
	// and sets the stream position to that value.
	RestoreMark()
	// DiscardMark pops a position from the marked indexes stack
	// without changing the stream position.
	DiscardMark()
}

type bufferedScanner struct {
	*Scanner
	buffer tokenSlice
}

func newBufferedScanner(s *Scanner) *bufferedScanner {
	return &bufferedScanner{
		Scanner: s,
		buffer: tokenSlice{
			// Usually only need 1 token of lookahead.
			tokens: make([]Token, 0, 1),
			marks:  make([]int, 0, 1),
		},
	}
}

func (s *bufferedScanner) Next() (Token, error) {
	if s.buffer.pos < len(s.buffer.tokens) {
		tok, err := s.buffer.Next()
		s.discardUnreachableBuffer()
		return tok, err
	}
	tok, err := s.Scanner.Next()
	if tok.Kind != EOFKind && len(s.buffer.marks) > 0 {
		s.buffer.tokens = append(s.buffer.tokens, tok)
		s.buffer.pos = len(s.buffer.tokens)
	}
	return tok, err
}

func (s *bufferedScanner) Mark() {
	s.buffer.Mark()
}

func (s *bufferedScanner) RestoreMark() {
	s.buffer.RestoreMark()
	s.discardUnreachableBuffer()
}

func (s *bufferedScanner) DiscardMark() {
	s.buffer.DiscardMark()
	s.discardUnreachableBuffer()
}

func (s *bufferedScanner) discardUnreachableBuffer() {
	if len(s.buffer.marks) == 0 {
		s.buffer.tokens = slices.Delete(s.buffer.tokens, 0, s.buffer.pos)
		s.buffer.pos = 0
	}
}

type tokenSlice struct {
	tokens []Token
	pos    int
	marks  []int
}

func (slice *tokenSlice) Next() (Token, error) {
	if slice.pos >= len(slice.tokens) {
		return Token{}, io.EOF
	}
	tok := slice.tokens[slice.pos]
	if tok.Kind == EOFKind {
		return tok, io.EOF
	}
	slice.pos++
	return tok, nil
}

func (slice *tokenSlice) Mark() {
	slice.marks = append(slice.marks, slice.pos)
}

func (slice *tokenSlice) RestoreMark() {
	slice.pos = slice.marks[len(slice.marks)-1]
	slice.DiscardMark()
}

func (slice *tokenSlice) DiscardMark() {
	slice.marks = slice.marks[:len(slice.marks)-1]
}
