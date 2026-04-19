//go:generate go tool stringer -linecomment -output=token_string.go -type=Kind token.go

package css

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Token is a single [CSS token].
//
// [CSS token]: https://www.w3.org/TR/css-syntax-3/#tokenizing-and-parsing
type Token struct {
	Kind  Kind
	Value string
	// Unit is the unit of the value for [DimensionKind].
	Unit string
	// Start is the location of the first byte of the token.
	Start Location
}

// IsIDHash reports whether the token is a [hash token]
// with the "id" type flag.
//
// [hash token]: https://www.w3.org/TR/css-syntax-3/#typedef-hash-token
func (tok Token) IsIDHash() bool {
	return tok.Kind == HashKind && startsWithIdentSequence([]rune(tok.Value))
}

// IsUnrestrictedHash reports whether the token is a [hash token]
// with the "unrestricted" type flag.
//
// [hash token]: https://www.w3.org/TR/css-syntax-3/#typedef-hash-token
func (tok Token) IsUnrestrictedHash() bool {
	return tok.Kind == HashKind && !tok.IsIDHash()
}

// IsInteger reports whether the token is a [NumberKind] or [DimensionKind] with an integral value.
func (tok Token) IsInteger() bool {
	return (tok.Kind == NumberKind || tok.Kind == DimensionKind) &&
		!strings.ContainsAny(tok.Value, ".eE")
}

// String formats the token in CSS syntax.
func (tok Token) String() string {
	switch tok.Kind {
	case EOFKind:
		return "/*EOF*/"
	case IdentKind:
		// TODO(soon): Escape.
		return tok.Value
	case WhitespaceKind:
		return "/*space*/"
	case FunctionKind:
		// TODO(soon): Escape.
		return tok.Value + "("
	case AtKeywordKind:
		// TODO(soon): Escape.
		return "@" + tok.Value
	case HashKind:
		// TODO(soon): Escape.
		return "#" + tok.Value
	case StringKind, BadStringKind:
		// TODO(maybe): Any other escapes?
		return `"` + strings.ReplaceAll(tok.Value, `"`, `\"`) + `"`
	case URLKind, BadURLKind:
		// TODO(soon): Escape.
		return "url(" + tok.Value + ")"
	case DelimKind:
		return tok.Value
	case NumberKind:
		return tok.Value
	case PercentageKind:
		return tok.Value + "%"
	case DimensionKind:
		// TODO(soon): Escape.
		return tok.Value + tok.Unit
	case CDOKind:
		return "<!--"
	case CDCKind:
		return "-->"
	case ColonKind:
		return ":"
	case SemicolonKind:
		return ";"
	case CommaKind:
		return ","
	case LBracketKind:
		return "["
	case RBracketKind:
		return "]"
	case LParenKind:
		return "("
	case RParenKind:
		return ")"
	case LBraceKind:
		return "{"
	case RBraceKind:
		return "}"
	default:
		return fmt.Sprintf("/*kind=%v value=%s*/", tok.Kind, tok.Value)
	}
}

// Kind is an enumeration of [Token] types.
type Kind int

// Defined [Kind] values.
const (
	EOFKind        Kind = iota // EOF
	IdentKind                  // ident
	WhitespaceKind             // whitespace
	FunctionKind               // function
	AtKeywordKind              // at-keyword
	HashKind                   // hash
	StringKind                 // string
	BadStringKind              // bad-string
	URLKind                    // url
	BadURLKind                 // bad-url
	DelimKind                  // delim
	NumberKind                 // number
	PercentageKind             // percentage
	DimensionKind              // dimension
	CDOKind                    // CDO
	CDCKind                    // CDC
	ColonKind                  // colon
	SemicolonKind              // semicolon
	CommaKind                  // comma
	LBracketKind               // [
	RBracketKind               // ]
	LParenKind                 // (
	RParenKind                 // )
	LBraceKind                 // {
	RBraceKind                 // }
)

// Location gives position information in a stream.
type Location struct {
	// Offset is the offset of the byte
	// relative to the beginning of the stream.
	Offset int64
	// Line is the 1-based line number of the location.
	Line int64
}

const maxTokenSize = 1024

// tokenBuilder builds a string and limits the length to [maxTokenSize].
type tokenBuilder struct {
	strings.Builder
	err error
}

func (tb *tokenBuilder) Reset() {
	tb.Builder.Reset()
	tb.err = nil
}

func (tb *tokenBuilder) String() (string, error) {
	return tb.Builder.String(), tb.err
}

func (tb *tokenBuilder) Write(p []byte) (int, error) {
	if tb.err != nil {
		return 0, tb.err
	}
	if maxLen := maxTokenSize - tb.Len(); len(p) > maxLen {
		tb.Builder.Write(p[:maxLen])
		tb.err = errLargeToken
		return maxLen, tb.err
	}
	tb.Builder.Write(p)
	return len(p), nil
}

func (tb *tokenBuilder) WriteByte(c byte) error {
	switch {
	case tb.err != nil:
		return tb.err
	case tb.Len() >= maxTokenSize:
		tb.err = errLargeToken
		return tb.err
	default:
		tb.Builder.WriteByte(c)
		return nil
	}
}

func (tb *tokenBuilder) WriteRune(r rune) (int, error) {
	if tb.err != nil {
		return 0, tb.err
	}
	if maxLen := maxTokenSize - tb.Len(); maxLen < utf8.UTFMax {
		var buf [utf8.UTFMax]byte
		n := utf8.EncodeRune(buf[:], r)
		if n > maxLen {
			tb.Builder.Write(buf[:maxLen])
			tb.err = errLargeToken
			return maxLen, tb.err
		}
		tb.Builder.Write(buf[:n])
		return n, nil
	}
	return tb.Builder.WriteRune(r)
}

func (tb *tokenBuilder) WriteString(s string) (int, error) {
	if tb.err != nil {
		return 0, tb.err
	}
	if maxLen := maxTokenSize - tb.Len(); len(s) > maxLen {
		tb.Builder.WriteString(s[:maxLen])
		tb.err = errLargeToken
		return maxLen, tb.err
	}
	tb.Builder.WriteString(s)
	return len(s), nil
}

var errLargeToken = errors.New("token too large")
