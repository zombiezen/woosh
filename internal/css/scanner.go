package css

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"unicode"
	"unicode/utf16"
)

// A Scanner splits an CSS input stream into tokens.
type Scanner struct {
	r *bufferedReader
}

// NewScanner returns a new [*Scanner] that reads from r.
// The scanner introduces its own buffering
// and may read data from r beyond the tokens requested.
func NewScanner(r io.RuneReader) *Scanner {
	return &Scanner{newBufferedReader(r)}
}

// Next returns the next CSS token in the input stream.
// At the end of the input stream, Next returns a [EOFKind] [Token]
// and the error returned from the [io.RuneReader].
// Next may also return errors along with other token kinds —
// such errors are [parse errors].
//
// [parse errors]: https://www.w3.org/TR/css-syntax-3/#parse-error
func (s *Scanner) Next() (Token, error) {
	if err := s.comments(); err != nil {
		return Token{}, err
	}
	start := s.r.location()
	r, _, err := s.r.ReadRune()
	if err != nil {
		return Token{}, err
	}
	switch {
	case isWhitespace(r):
		s.consumeWhitespace()
		return Token{Kind: WhitespaceKind, Start: start}, nil
	case r == '"' || r == '\'':
		return s.string(start, r)
	case r == '#':
		if next, _ := s.r.peek(2); len(next) >= 1 && isIdent(next[0]) || startsWithValidEscape(next) {
			tb := new(tokenBuilder)
			s.consumeIdent(tb)
			value, err := tb.String()
			if err != nil {
				err = fmt.Errorf("parse identifier: %w", err)
			}
			return Token{
				Kind:  HashKind,
				Value: value,
				Start: start,
			}, err
		}
		return Token{
			Kind:  DelimKind,
			Value: "#",
			Start: start,
		}, nil
	case r == '(':
		return Token{Kind: LParenKind, Start: start}, nil
	case r == ')':
		return Token{Kind: RParenKind, Start: start}, nil
	case r == '+':
		s.r.UnreadRune()
		if s.startsWithNumber() {
			return s.numeric()
		}
		s.r.ReadRune()
		return Token{Kind: DelimKind, Value: "+", Start: start}, nil
	case r == ',':
		return Token{Kind: CommaKind, Start: start}, nil
	case r == '-':
		s.r.UnreadRune()
		if s.startsWithNumber() {
			return s.numeric()
		}
		if s.consumeLiteral("-->") {
			return Token{Kind: CDCKind, Start: start}, nil
		}
		if s.startsWithIdentSequence() {
			return s.ident()
		}
		s.r.ReadRune()
		return Token{
			Kind:  DelimKind,
			Value: "-",
			Start: start,
		}, nil
	case r == '.':
		s.r.UnreadRune()
		if s.startsWithNumber() {
			return s.numeric()
		}
		s.r.ReadRune()
		return Token{
			Kind:  DelimKind,
			Value: ".",
			Start: start,
		}, nil
	case r == ':':
		return Token{Kind: ColonKind, Start: start}, nil
	case r == ';':
		return Token{Kind: SemicolonKind, Start: start}, nil
	case r == '<':
		if s.consumeLiteral("!--") {
			return Token{Kind: CDOKind, Start: start}, nil
		}
		return Token{Kind: DelimKind, Value: "<", Start: start}, nil
	case r == '@':
		if s.startsWithIdentSequence() {
			tb := new(tokenBuilder)
			s.consumeIdent(tb)
			value, err := tb.String()
			if err != nil {
				err = fmt.Errorf("parse at-keyword: %w", err)
			}
			return Token{
				Kind:  AtKeywordKind,
				Value: value,
				Start: start,
			}, err
		}
		return Token{Kind: DelimKind, Value: "@", Start: start}, nil
	case r == '[':
		return Token{Kind: LBracketKind, Start: start}, nil
	case r == '\\':
		s.r.UnreadRune()
		if !s.startsWithValidEscape() {
			_, err := s.consumeEscapedCodePoint()
			return Token{
				Kind:  DelimKind,
				Value: "\\",
				Start: start,
			}, err
		}
		return s.ident()
	case r == ']':
		return Token{Kind: RBracketKind, Start: start}, nil
	case r == '{':
		return Token{Kind: LBraceKind, Start: start}, nil
	case r == '}':
		return Token{Kind: RBraceKind, Start: start}, nil
	case isDigit(r):
		s.r.UnreadRune()
		return s.numeric()
	case isIdentStart(r):
		s.r.UnreadRune()
		return s.ident()
	default:
		return Token{Kind: DelimKind, Value: string(r), Start: start}, nil
	}
}

func (s *Scanner) ident() (Token, error) {
	start := s.r.location()
	tb := new(tokenBuilder)
	s.consumeIdent(tb)

	if next, _ := s.r.peek(1); len(next) >= 1 && next[0] == '(' {
		s.r.ReadRune()
		value, err := tb.String()
		if err != nil {
			err = fmt.Errorf("parse function: %w", err)
		} else if EqualCaseInsensitive(value, "url") {
			return s.url(tb, start)
		}
		return Token{
			Kind:  FunctionKind,
			Value: value,
			Start: start,
		}, err
	}

	value, err := tb.String()
	if err != nil {
		err = fmt.Errorf("parse identifier: %w", err)
	}
	return Token{
		Kind:  IdentKind,
		Value: value,
		Start: start,
	}, err
}

func (s *Scanner) url(tb *tokenBuilder, start Location) (Token, error) {
	for {
		if next, _ := s.r.peek(2); len(next) >= 2 && isWhitespace(next[0]) && isWhitespace(next[1]) {
			s.r.ReadRune()
		} else {
			break
		}
	}
	if next, _ := s.r.peek(2); len(next) >= 1 && (next[0] == '"' || next[0] == '\'') ||
		len(next) >= 2 && isWhitespace(next[0]) && (next[1] == '"' || next[1] == '\'') {
		value, err := tb.String()
		if err != nil {
			err = fmt.Errorf("parse function: %w", err)
		}
		return Token{
			Kind:  FunctionKind,
			Value: value,
			Start: start,
		}, err
	}

	// Confirmed that it's a URL token.
	tb.Reset()
	s.consumeWhitespace()
	var parseError error
urlChars:
	for {
		r, _, err := s.r.ReadRune()
		if err != nil {
			err = fmt.Errorf("parse url: %w", err)
			value, _ := tb.String()
			return Token{
				Kind:  URLKind,
				Value: value,
				Start: start,
			}, err
		}
		switch {
		case r == ')':
			value, err := tb.String()
			if err != nil {
				err = fmt.Errorf("parse url: %w", err)
			}
			return Token{
				Kind:  URLKind,
				Value: value,
				Start: start,
			}, err
		case isWhitespace(r):
			s.consumeWhitespace()
			r, _, err := s.r.ReadRune()
			if err != nil || r == ')' {
				value, valueError := tb.String()
				err = fmt.Errorf("parse url: %w", cmp.Or(err, valueError))
				return Token{
					Kind:  URLKind,
					Value: value,
				}, err
			}
			tb.WriteRune(r)
			break urlChars
		case r == '"' || r == '\'' || r == '(' || isNonPrintable(r):
			tb.WriteRune(r)
			parseError = fmt.Errorf("parse url: expected url character (found %q)", r)
			break urlChars
		case r == '\\':
			s.r.UnreadRune()
			r, err := s.consumeEscapedCodePoint()
			if err != nil {
				parseError = fmt.Errorf("parse url: %w", err)
				break urlChars
			}
			tb.WriteRune(r)
		default:
			tb.WriteRune(r)
		}
	}

	// Consume the rest of a bad URL.
	for {
		r, _, err := s.r.ReadRune()
		if err != nil || r == ')' {
			value, _ := tb.String()
			return Token{
				Kind:  BadURLKind,
				Value: value,
				Start: start,
			}, parseError
		}
		if r == '\\' {
			r, _ = s.consumeEscapedCodePoint()
		}
		tb.WriteRune(r)
	}
}

func (s *Scanner) consumeIdent(tb *tokenBuilder) {
	for {
		r, _, err := s.r.ReadRune()
		if err != nil {
			return
		}
		switch {
		case isIdent(r):
			tb.WriteRune(r)
		case r == '\\':
			s.r.UnreadRune()
			if s.startsWithValidEscape() {
				r, err := s.consumeEscapedCodePoint()
				if err != nil {
					panic(err)
				}
				tb.WriteRune(r)
				continue
			}
		default:
			s.r.UnreadRune()
			return
		}
	}
}

func (s *Scanner) startsWithIdentSequence() bool {
	next, _ := s.r.peek(3)
	return startsWithIdentSequence(next)
}

func (s *Scanner) string(start Location, end rune) (Token, error) {
	tb := new(tokenBuilder)
	for {
		r, _, err := s.r.ReadRune()
		if err != nil {
			err = fmt.Errorf("parse string: %w", err)
			value, _ := tb.String()
			return Token{
				Kind:  StringKind,
				Value: value,
				Start: start,
			}, err
		}
		switch r {
		case end:
			value, err := tb.String()
			if err != nil {
				err = fmt.Errorf("parse string: %w", err)
			}
			return Token{
				Kind:  StringKind,
				Value: value,
				Start: start,
			}, err
		case '\n':
			s.r.UnreadRune()
			err = fmt.Errorf("parse string: expected %c (found newline)", end)
			value, _ := tb.String()
			return Token{
				Kind:  BadStringKind,
				Value: value,
				Start: start,
			}, err
		case '\\':
			s.r.UnreadRune()
			if next, _ := s.r.peek(2); !startsWithValidEscape(next) {
				for range next {
					s.r.ReadRune()
				}
				continue
			}
			r, err := s.consumeEscapedCodePoint()
			if err != nil {
				err = fmt.Errorf("parse string: %w", err)
				value, _ := tb.String()
				return Token{
					Kind:  StringKind,
					Value: value,
					Start: start,
				}, err
			}
			tb.WriteRune(r)
		default:
			tb.WriteRune(r)
		}
	}
}

func (s *Scanner) numeric() (Token, error) {
	start := s.r.location()
	tb := new(tokenBuilder)
	if err := s.consumeNumber(tb); err != nil {
		value, _ := tb.String()
		return Token{
			Kind:  NumberKind,
			Value: value,
			Start: start,
		}, err
	}
	if s.startsWithIdentSequence() {
		n := tb.Len()
		s.consumeIdent(tb)
		value, err := tb.String()
		if err != nil {
			err = fmt.Errorf("parse dimension: %w", err)
		}
		return Token{
			Kind:  DimensionKind,
			Value: value[:n],
			Unit:  value[n:],
			Start: start,
		}, err
	}
	if next, _ := s.r.peek(1); len(next) >= 1 && next[0] == '%' {
		s.r.ReadRune()
		value, err := tb.String()
		if err != nil {
			err = fmt.Errorf("parse percentage: %w", err)
		}
		return Token{
			Kind:  PercentageKind,
			Value: value,
			Start: start,
		}, err
	}

	value, err := tb.String()
	if err != nil {
		err = fmt.Errorf("parse number: %w", err)
	}
	return Token{
		Kind:  NumberKind,
		Value: value,
		Start: start,
	}, err
}

func (s *Scanner) consumeNumber(tb *tokenBuilder) error {
	// Consume sign.
	r, _, err := s.r.ReadRune()
	if err != nil {
		return fmt.Errorf("parse number: %w", err)
	}
	if r == '+' || r == '-' {
		tb.WriteRune(r)
		r, _, err = s.r.ReadRune()
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return fmt.Errorf("parse number: %w", err)
		}
	}

	// Consume integer part.
	hasDigits := isDigit(r)
	for isDigit(r) {
		tb.WriteRune(r)
		r, _, err = s.r.ReadRune()
		if err != nil {
			// Already valid.
			return nil
		}
	}

	// Fractional component.
	if next, _ := s.r.peek(1); len(next) >= 1 && r == '.' && isDigit(next[0]) {
		hasDigits = true
		tb.WriteRune('.')
		tb.WriteRune(next[0])
		s.r.ReadRune()
		for {
			r, _, err := s.r.ReadRune()
			if err != nil {
				// Already valid.
				return nil
			}
			if !isDigit(r) {
				s.r.UnreadRune()
				break
			}
			tb.WriteRune(r)
		}
	} else {
		s.r.UnreadRune()
	}

	if !hasDigits {
		next, err := s.r.peek(1)
		switch err {
		case nil:
			err = fmt.Errorf("expected digit or '.' (got %q)", next[0])
		case io.EOF:
			err = io.ErrUnexpectedEOF
		}
		return fmt.Errorf("parse number: %w", err)
	}

	// Optional exponent.
	if next, _ := s.r.peek(3); len(next) >= 2 && (next[0] == 'e' || next[0] == 'E') {
		var n int
		switch {
		case isDigit(next[1]):
			n = 2
		case len(next) >= 3 && (next[1] == '-' || next[1] == '+') && isDigit(next[2]):
			n = 3
		}
		if n > 0 {
			for _, r := range next[:n] {
				tb.WriteRune(r)
			}
			for range n {
				s.r.ReadRune()
			}
			for {
				r, _, err := s.r.ReadRune()
				if err != nil {
					break
				}
				if !isDigit(r) {
					s.r.UnreadRune()
					break
				}
				tb.WriteRune(r)
			}
		}
	}

	return nil
}

func (s *Scanner) startsWithNumber() bool {
	next, _ := s.r.peek(3)
	return startsWithNumber(next)
}

func (s *Scanner) consumeEscapedCodePoint() (rune, error) {
	next, err := s.r.peek(2)
	if err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return unicode.ReplacementChar, fmt.Errorf("parse escaped code point: %w", err)
	}
	if next[0] != '\\' {
		return unicode.ReplacementChar, fmt.Errorf("parse escaped code point: expected '\\' (found %q)", next[0])
	}
	r := next[1]

	if !startsWithValidEscape(next) {
		s.r.ReadRune() // Consume backslash.
		return unicode.ReplacementChar, fmt.Errorf("parse escaped code point: found %q after '\\'", r)
	}
	// next no longer valid beyond here.
	s.r.ReadRune()
	s.r.ReadRune()

	digit, ok := hexDigit(r)
	if !ok {
		return r, nil
	}
	x := rune(digit)
	for range 5 {
		r, _, err := s.r.ReadRune()
		if err != nil {
			break
		}
		digit, ok := hexDigit(r)
		if !ok {
			s.r.UnreadRune()
			break
		}
		x <<= 4
		x |= rune(digit)
	}

	if next, _ := s.r.peek(1); len(next) >= 1 && isWhitespace(next[0]) {
		s.r.ReadRune()
	}

	if x == 0 || x > unicode.MaxRune || utf16.IsSurrogate(x) {
		return unicode.ReplacementChar, nil
	}
	return x, nil
}

func (s *Scanner) startsWithValidEscape() bool {
	next, _ := s.r.peek(2)
	return startsWithValidEscape(next)
}

func (s *Scanner) comments() error {
	for s.consumeLiteral("/*") {
		for !s.consumeLiteral("*/") {
			if _, _, err := s.r.ReadRune(); err != nil {
				if err == io.EOF {
					err = io.ErrUnexpectedEOF
				}
				return fmt.Errorf("parse css comment: %w", err)
			}
		}
	}
	return nil
}

func (s *Scanner) consumeWhitespace() {
	for {
		r, _, err := s.r.ReadRune()
		if err != nil {
			return
		}
		if !isWhitespace(r) {
			s.r.UnreadRune()
			return
		}
	}
}

func (s *Scanner) consumeLiteral(lit string) bool {
	next, err := s.r.peek(int8(len(lit)))
	if err != nil {
		return false
	}
	for _, want := range lit {
		if next[0] != want {
			return false
		}
		next = next[1:]
	}
	for range len(lit) {
		s.r.ReadRune()
	}
	return true
}

const scannerLookAhead = 4

type bufferedReader struct {
	r       io.RuneReader
	rpos    int8
	wpos    int8
	buf     [scannerLookAhead]rune
	sizes   [scannerLookAhead]int
	baseLoc Location
	err     error
}

func newBufferedReader(r io.RuneReader) *bufferedReader {
	return &bufferedReader{
		r:       r,
		baseLoc: Location{Line: 1},
	}
}

func (br *bufferedReader) location() Location {
	return addLocation(br.baseLoc, br.buf[:br.rpos], br.sizes[:br.rpos])
}

// ReadRune reads the next [filtered code point] in the input stream.
//
// [filtered code point]: https://www.w3.org/TR/css-syntax-3/#input-preprocessing
func (br *bufferedReader) ReadRune() (r rune, size int, err error) {
	if _, err := br.peek(1); err != nil {
		return 0, 0, err
	}
	r = br.buf[br.rpos]
	size = br.sizes[br.rpos]
	br.rpos++
	return
}

func (br *bufferedReader) UnreadRune() error {
	if br.rpos <= 0 {
		return errors.New("invalid use of UnreadRune")
	}
	br.rpos--
	return nil
}

// peek returns the next n runes without advancing the reader.
// The runes stop being valid at the next read call.
// If necessary, peek will read more runes into the buffer in order to make n runes available.
// If peek returns fewer than n runes,
// it also returns an error explaining why the read is short.
func (br *bufferedReader) peek(n int8) ([]rune, error) {
	if n > scannerLookAhead-1 {
		return nil, fmt.Errorf("cannot look ahead %d runes", n)
	}
	toRead := n - (br.wpos - br.rpos)
	if toRead <= 0 {
		return br.buf[br.rpos : br.rpos+n], nil
	}
	if br.err != nil {
		return br.buf[br.rpos:br.wpos], br.err
	}

	var buf [scannerLookAhead]rune
	var sizes [scannerLookAhead]int
	nread := int8(0)
	for nread < toRead {
		r, size, err := br.r.ReadRune()
		if err != nil {
			br.err = err
			break
		}
		switch {
		case r == '\r':
			r = '\n'
			r2, size2, err := br.r.ReadRune()
			if err != nil {
				br.err = err
			} else if r2 == '\n' {
				size += size2
			} else {
				buf[nread] = '\n'
				buf[nread+1] = r2
				sizes[nread] = size
				sizes[nread+1] = size2
				nread += 2
				continue
			}
		case r == '\f':
			r = '\n'
		case r == 0 || utf16.IsSurrogate(r):
			r = unicode.ReplacementChar
		}
		buf[nread] = r
		sizes[nread] = size
		nread++
	}

	// Pop excess runes from the front.
	popCount := max(br.wpos+nread-scannerLookAhead, 0)
	br.baseLoc = addLocation(br.baseLoc, br.buf[:popCount], br.sizes[:popCount])
	copy(br.buf[:], br.buf[popCount:])
	copy(br.sizes[:], br.sizes[popCount:])
	br.rpos -= popCount
	br.wpos -= popCount

	// Push runes to the end.
	copy(br.buf[br.wpos:], buf[:nread])
	copy(br.sizes[br.wpos:], sizes[:nread])
	br.wpos += nread

	wantEnd := br.rpos + n
	if wantEnd > br.wpos {
		return br.buf[br.rpos:br.wpos], br.err
	}
	return br.buf[br.rpos:wantEnd], nil
}

func isWhitespace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n'
}

func isIdentStart(r rune) bool {
	return 'A' <= r && r <= 'Z' ||
		'a' <= r && r <= 'z' ||
		r == '_' ||
		!isASCII(r)
}

func isASCII(r rune) bool {
	return 0 <= r && r < 0x80
}

// startsWithIdentSequence reports whether the slice [starts with an ident sequence].
//
// [starts with an ident sequence]: https://www.w3.org/TR/css-syntax-3/#would-start-an-identifier
func startsWithIdentSequence(s []rune) bool {
	if len(s) == 0 {
		return false
	}
	switch s[0] {
	case '-':
		return len(s) >= 2 && (isIdentStart(s[1]) || s[1] == '-' || startsWithValidEscape(s[1:]))
	case '\\':
		return startsWithValidEscape(s)
	default:
		return isIdentStart(s[0])
	}
}

func isIdent(r rune) bool {
	return isIdentStart(r) || isDigit(r) || r == '-'
}

// startsWithValidEscape reports if the slice starts with a [valid escape].
//
// [valid escape]: https://www.w3.org/TR/css-syntax-3/#starts-with-a-valid-escape
func startsWithValidEscape(s []rune) bool {
	return len(s) >= 2 && s[0] == '\\' && s[1] != '\n'
}

// startsWithNumber reports if the slice [starts with a number].
//
// [starts with a number]: https://www.w3.org/TR/css-syntax-3/#check-if-three-code-points-would-start-a-number
func startsWithNumber(s []rune) bool {
	if len(s) == 0 {
		return false
	}
	switch s[0] {
	case '+', '-':
		return len(s) >= 2 && isDigit(s[1]) ||
			len(s) >= 3 && s[1] == '.' && isDigit(s[2])
	case '.':
		return len(s) >= 2 && isDigit(s[1])
	default:
		return isDigit(s[0])
	}
}

func isDigit(r rune) bool {
	return '0' <= r && r <= '9'
}

func hexDigit(r rune) (n byte, ok bool) {
	switch {
	case isDigit(r):
		return byte(r - '0'), true
	case 'A' <= r && r <= 'F':
		return byte(r - 'A' + 0xa), true
	case 'a' <= r && r <= 'f':
		return byte(r - 'a' + 0xa), true
	default:
		return 0, false
	}
}

func toASCIILower(r rune) rune {
	if 'A' <= r && r <= 'Z' {
		return r - 'A' + 'a'
	}
	return r
}

// EqualCaseInsensitive reports whether s1 is an
// [ASCII case-insensitive match] for s2.
//
// [ASCII case-insensitive match]: https://infra.spec.whatwg.org/#ascii-case-insensitive
func EqualCaseInsensitive(s1, s2 string) bool {
	if len(s1) != len(s2) {
		return false
	}
	for i, c1 := range []byte(s1) {
		c2 := s2[i]
		if toASCIILower(rune(c1)) != toASCIILower(rune(c2)) {
			return false
		}
	}
	return true
}

func isNonPrintable(r rune) bool {
	return 0 <= r && r <= '\b' ||
		r == '\t' ||
		0x0e <= r && r <= 0x1f ||
		r == 0x7f
}

func addLocation(loc Location, runes []rune, sizes []int) Location {
	for _, r := range runes {
		if r == '\n' {
			loc.Line++
		}
	}
	for _, size := range sizes {
		loc.Offset += int64(size)
	}
	return loc
}
