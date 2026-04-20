package css

import (
	"errors"
	"fmt"
	"io"
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
		stream:   &bufferedScanner{Scanner: s},
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
		tok, err := p.stream.Next()
		if err != nil {
			parseError = errors.Join(parseError, fmt.Errorf("parse css rule: %w", err))
		}
		switch tok.Kind {
		case EOFKind:
			return nil, parseError
		case WhitespaceKind:
			// Ignore.
		case CDOKind, CDCKind:
			if !p.topLevel {
				p.stream.Previous()
				r, err := p.qualifiedRule()
				parseError = errors.Join(parseError, err)
				if r != nil {
					return r, parseError
				}
			}
		case AtKeywordKind:
			r, err := p.atRule(tok.Value, tok.Start)
			parseError = errors.Join(parseError, err)
			if r != nil {
				return r, parseError
			}
		default:
			p.stream.Previous()
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
		tok, err := p.stream.Next()
		if err != nil {
			parseError = errors.Join(parseError, fmt.Errorf("parse css @%s rule: %w", name, err))
		}
		switch tok.Kind {
		case EOFKind, SemicolonKind:
			return r, parseError
		case LBraceKind:
			p.stream.Previous()
			var err error
			r.Block, err = p.block(nil)
			if err != nil {
				// TODO(soon): Split apart err and wrap each error individually.
				parseError = errors.Join(parseError, fmt.Errorf("parse css @%s rule: %w", name, err))
			}
			return r, parseError
		default:
			p.stream.Previous()
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
		tok, err := p.stream.Next()
		if err != nil {
			parseError = errors.Join(parseError, fmt.Errorf("parse css rule: %w", err))
		}
		switch tok.Kind {
		case EOFKind:
			return nil, parseError
		case LBraceKind:
			p.stream.Previous()
			var err error
			r.Block, err = p.block(nil)
			if err != nil {
				// TODO(soon): Split apart err and wrap each error individually.
				parseError = errors.Join(parseError, fmt.Errorf("parse css rule: %w", err))
			}
			return r, parseError
		default:
			p.stream.Previous()
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
	tok, err := p.stream.Next()
	if tok.Kind == EOFKind {
		return dst, err
	}
	switch tok.Kind {
	case LBraceKind, LBracketKind, LParenKind:
		p.stream.Previous()
		return p.block(dst)
	case FunctionKind:
		p.stream.Previous()
		return p.function(dst)
	default:
		dst = append(dst, tok)
		if err != nil {
			err = fmt.Errorf("parse css value: %w", err)
		}
		return dst, err
	}
}

func (p *Parser) block(dst []Token) ([]Token, error) {
	tok, err := p.stream.Next()
	if tok.Kind == EOFKind {
		return dst, err
	}
	name, end, isBlock := blockKind(tok.Kind)
	if !isBlock || tok.Kind == FunctionKind {
		p.stream.Previous()
		return dst, fmt.Errorf("parse css block: expected (/[/{ (found %v)", tok)
	}
	if err != nil {
		err = fmt.Errorf("parse css %s-block: %w", name, err)
	}
	parseError := err
	dst = append(dst, tok)

	for {
		tok, err := p.stream.Next()
		switch tok.Kind {
		case EOFKind:
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			parseError = errors.Join(parseError, fmt.Errorf("parse css %s-block: %w", name, err))
			return dst, parseError
		case end:
			dst = append(dst, tok)
			if err != nil {
				parseError = errors.Join(parseError, fmt.Errorf("parse css %s-block: %w", name, err))
			}
			return dst, parseError
		default:
			p.stream.Previous()
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
	tok, err := p.stream.Next()
	if tok.Kind == EOFKind {
		return dst, err
	}
	if tok.Kind != FunctionKind {
		p.stream.Previous()
		return dst, fmt.Errorf("parse css function call: expected %v (found %v)", FunctionKind, tok)
	}
	name := tok.Value
	if err != nil {
		err = fmt.Errorf("parse css %s call: %w", name, err)
	}
	parseError := err
	dst = append(dst, tok)

	for {
		tok, err := p.stream.Next()
		switch tok.Kind {
		case EOFKind:
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			parseError = errors.Join(parseError, fmt.Errorf("parse css %s call: %w", name, err))
			return dst, parseError
		case RParenKind:
			dst = append(dst, tok)
			if err != nil {
				parseError = errors.Join(parseError, fmt.Errorf("parse css %s call: %w", name, err))
			}
			return dst, parseError
		default:
			p.stream.Previous()
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
		tok, err := p.stream.Next()
		if err != nil {
			return
		}
		if tok.Kind != WhitespaceKind {
			p.stream.Previous()
			return
		}
	}
}

type tokenStream interface {
	Next() (Token, error)
	Previous()
}

type bufferedScanner struct {
	*Scanner
	buffer    Token
	err       error
	hasBuffer bool
}

func (s *bufferedScanner) Next() (Token, error) {
	if s.hasBuffer {
		s.hasBuffer = false
		return s.buffer, s.err
	}
	tok, err := s.Scanner.Next()
	if tok.Kind != EOFKind {
		s.buffer, s.err = tok, err
	}
	return tok, err
}

func (s *bufferedScanner) Previous() {
	if s.buffer.Kind != EOFKind {
		s.hasBuffer = true
	}
}

type tokenSlice struct {
	tokens []Token
	pos    int
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

func (slice *tokenSlice) Previous() {
	slice.pos--
}
