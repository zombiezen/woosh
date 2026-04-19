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
		isValidNumber(tok.Value) &&
		!strings.ContainsAny(tok.Value, ".eE")
}

// String formats the token in CSS syntax.
func (tok Token) String() string {
	if s, ok := kindSymbol(tok.Kind); ok {
		return s
	}
	if !isKnownKind(tok.Kind) {
		return fmt.Sprintf("/*kind=%v value=%s*/", tok.Kind, tok.Value)
	}

	switch tok.Kind {
	case EOFKind:
		return "/*EOF*/"
	case WhitespaceKind:
		return "/*space*/"
	default:
		sb := new(strings.Builder)
		NewWriter(sb).WriteToken(tok)
		return sb.String()
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

func isKnownKind(k Kind) bool {
	return EOFKind <= k && k <= RBraceKind
}

func kindSymbol(k Kind) (string, bool) {
	switch k {
	case CDOKind:
		return "<!--", true
	case CDCKind:
		return "-->", true
	case ColonKind:
		return ":", true
	case SemicolonKind:
		return ";", true
	case CommaKind:
		return ",", true
	case LBracketKind:
		return "[", true
	case RBracketKind:
		return "]", true
	case LParenKind:
		return "(", true
	case RParenKind:
		return ")", true
	case LBraceKind:
		return "{", true
	case RBraceKind:
		return "}", true
	default:
		return "", false
	}
}

// Location gives position information in a stream.
type Location struct {
	// Offset is the offset of the byte
	// relative to the beginning of the stream.
	Offset int64
	// Line is the 1-based line number of the location.
	Line int64
}

func isValidNumber(s string) bool {
	if len(s) >= 1 && (s[0] == '+' || s[0] == '-') {
		s = s[1:]
	}
	if rest, startsWithDigits := cutDigitPrefix(s); startsWithDigits {
		s = rest
		if len(s) >= 1 && s[0] == '.' {
			var hasFraction bool
			s, hasFraction = cutDigitPrefix(s[1:])
			if !hasFraction {
				return false
			}
		}
	} else if len(s) >= 2 && s[0] == '.' && isDigit(rune(s[1])) {
		s = s[2:]
		s, _ = cutDigitPrefix(s)
	} else {
		return false
	}
	if len(s) >= 1 && (s[0] == 'e' || s[0] == 'E') {
		s = s[1:]
		if len(s) >= 1 && (s[0] == '+' || s[0] == '-') {
			s = s[1:]
		}
		var hasExponent bool
		s, hasExponent = cutDigitPrefix(s)
		if !hasExponent {
			return false
		}
	}
	return len(s) == 0
}

func cutDigitPrefix(s string) (string, bool) {
	cut := false
	for len(s) >= 1 && isDigit(rune(s[0])) {
		s = s[1:]
		cut = true
	}
	return s, cut
}

func isValidDelimiter(s string) bool {
	if len(s) != 1 {
		return false
	}
	r := s[0]
	return isASCII(rune(r)) &&
		!isIdentStart(rune(r)) &&
		!isDigit(rune(r)) &&
		strings.IndexByte(`"'(){}[],:;`, r) == -1
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
