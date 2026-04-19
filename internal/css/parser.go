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
	nextFunc  func() (Token, error)
	buffer    Token
	err       error
	hasBuffer bool
	topLevel  bool
}

// NewParser returns a new [*Parser] that reads tokens from the given [*Scanner].
func NewParser(s *Scanner) *Parser {
	return &Parser{nextFunc: s.Next, topLevel: true}
}

// ParseTokens returns a new [*Parser] that reads tokens from the given slice.
func ParseTokens(tokens []Token) *Parser {
	i := 0
	return &Parser{nextFunc: func() (Token, error) {
		if i >= len(tokens) {
			return Token{}, io.EOF
		}
		tok := tokens[i]
		if tok.Kind == EOFKind {
			return tok, io.EOF
		}
		i++
		return tok, nil
	}}
}

func (p *Parser) next() (Token, error) {
	if p.hasBuffer {
		p.hasBuffer = false
		return p.buffer, p.err
	}
	tok, err := p.nextFunc()
	if tok.Kind != EOFKind {
		p.buffer, p.err = tok, err
	}
	return tok, err
}

func (p *Parser) prev() {
	if p.buffer.Kind != EOFKind {
		p.hasBuffer = true
	}
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
		tok, err := p.next()
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
				p.prev()
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
			p.prev()
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
		tok, err := p.next()
		if err != nil {
			parseError = errors.Join(parseError, fmt.Errorf("parse css @%s rule: %w", name, err))
		}
		switch tok.Kind {
		case EOFKind, SemicolonKind:
			return r, parseError
		case LBraceKind:
			p.prev()
			var err error
			r.Block, err = p.block(nil)
			if err != nil {
				// TODO(soon): Split apart err and wrap each error individually.
				parseError = errors.Join(parseError, fmt.Errorf("parse css @%s rule: %w", name, err))
			}
			return r, parseError
		default:
			p.prev()
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
		tok, err := p.next()
		if err != nil {
			parseError = errors.Join(parseError, fmt.Errorf("parse css rule: %w", err))
		}
		switch tok.Kind {
		case EOFKind:
			return nil, parseError
		case LBraceKind:
			p.prev()
			var err error
			r.Block, err = p.block(nil)
			if err != nil {
				// TODO(soon): Split apart err and wrap each error individually.
				parseError = errors.Join(parseError, fmt.Errorf("parse css rule: %w", err))
			}
			return r, parseError
		default:
			p.prev()
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
	tok, err := p.next()
	if tok.Kind == EOFKind {
		return dst, err
	}
	switch tok.Kind {
	case LBraceKind, LBracketKind, LParenKind:
		p.prev()
		return p.block(dst)
	case FunctionKind:
		p.prev()
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
	tok, err := p.next()
	if tok.Kind == EOFKind {
		return dst, err
	}
	var name string
	var end Kind
	switch tok.Kind {
	case LParenKind:
		name = "()"
		end = RParenKind
	case LBracketKind:
		name = "[]"
		end = RBracketKind
	case LBraceKind:
		name = "{}"
		end = RBraceKind
	default:
		p.prev()
		return dst, fmt.Errorf("parse css block: expected (/[/{ (found %v)", tok)
	}
	if err != nil {
		err = fmt.Errorf("parse css %s-block: %w", name, err)
	}
	parseError := err
	dst = append(dst, tok)

	for {
		tok, err := p.next()
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
			p.prev()
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
	tok, err := p.next()
	if tok.Kind == EOFKind {
		return dst, err
	}
	if tok.Kind != FunctionKind {
		p.prev()
		return dst, fmt.Errorf("parse css function call: expected %v (found %v)", FunctionKind, tok)
	}
	name := tok.Value
	if err != nil {
		err = fmt.Errorf("parse css %s call: %w", name, err)
	}
	parseError := err
	dst = append(dst, tok)

	for {
		tok, err := p.next()
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
			p.prev()
			var err error
			dst, err = p.value(dst)
			if err != nil {
				// TODO(soon): Split apart err and wrap each error individually.
				parseError = errors.Join(parseError, fmt.Errorf("parse css %s call: %w", name, err))
			}
		}
	}
}

// A Rule is a set of tokens followed by a {}-block.
// It may optionally start with an at-keyword.
type Rule struct {
	AtRule     string
	AtLocation Location
	Prelude    []Token
	Block      []Token
}

// AtKeyword returns the rule's at-keyword token, if present.
func (rule *Rule) AtKeyword() (Token, bool) {
	if rule.AtRule == "" {
		return Token{}, false
	}
	return Token{
		Kind:  AtKeywordKind,
		Value: rule.AtRule,
		Start: rule.AtLocation,
	}, true
}
