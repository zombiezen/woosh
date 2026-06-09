// Copyright 2026 Roxy Light
// SPDX-License-Identifier: MIT

package css

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"
)

// A Writer writes CSS tokens to an [io.Writer].
type Writer struct {
	w         stringWriter
	prevKind  Kind
	prevValue string

	indent      []byte
	needsIndent bool
}

// NewWriter returns a new [*Writer] that writes to w.
func NewWriter(w io.Writer) *Writer {
	sw, ok := w.(stringWriter)
	if !ok {
		sw = fallbackStringWriter{w}
	}
	return &Writer{
		w:      sw,
		indent: make([]byte, 0, 8),
	}
}

// WriteToken writes the token to the underlying writer.
func (w *Writer) WriteToken(tok Token) (err error) {
	if tok.Kind == WhitespaceKind {
		switch w.prevKind {
		case WhitespaceKind:
			return nil
		case SemicolonKind, RBraceKind:
			// No indentation change.
		case LBraceKind:
			w.indent = append(w.indent, '\t')
		default:
			w.prevKind = WhitespaceKind
			w.prevValue = ""
			_, err := w.w.WriteString(" ")
			return err
		}
		w.prevKind = WhitespaceKind
		w.prevValue = ""
		w.needsIndent = true
		_, err := w.w.WriteString("\n")
		return err
	}

	if tok.Kind == RBraceKind {
		w.indent = w.indent[:max(len(w.indent)-1, 0)]
	}
	if w.needsIndent {
		w.needsIndent = false
		if len(w.indent) > 0 {
			if _, err := w.w.Write(w.indent); err != nil {
				return err
			}
		}
	}
	if requiresCommentSeparator(Token{Kind: w.prevKind, Value: w.prevValue}, tok) {
		if _, err := w.w.WriteString("/**/"); err != nil {
			return err
		}
	}
	w.prevKind = tok.Kind
	w.prevValue = tok.Value

	if s, ok := kindSymbol(tok.Kind); ok {
		_, err := w.w.WriteString(s)
		return err
	}

	switch tok.Kind {
	case IdentKind:
		return writeIdent(w.w, tok.Value)
	case FunctionKind:
		if err := writeIdent(w.w, tok.Value); err != nil {
			return err
		}
		if _, err := w.w.WriteString("("); err != nil {
			return err
		}
	case AtKeywordKind:
		if _, err := w.w.WriteString("@"); err != nil {
			return err
		}
		if err := writeIdent(w.w, tok.Value); err != nil {
			return err
		}
	case HashKind:
		if _, err := w.w.WriteString("#"); err != nil {
			return err
		}
		// TODO(maybe): Is this the right escaping?
		if err := writeIdent(w.w, tok.Value); err != nil {
			return err
		}
	case StringKind, BadStringKind:
		if _, err := w.w.WriteString(`"`); err != nil {
			return err
		}
		err := escape(w.w, tok.Value, func(r rune) bool {
			return r == '"' || r == '\\' || r == '\n'
		})
		if err != nil {
			return err
		}
		if _, err := w.w.WriteString(`"`); err != nil {
			return err
		}
	case URLKind, BadURLKind:
		if _, err := w.w.WriteString("url("); err != nil {
			return err
		}
		err := escape(w.w, tok.Value, func(r rune) bool {
			return isWhitespace(r) || strings.ContainsRune(`"'()\`, r) || isNonPrintable(r)
		})
		if err != nil {
			return err
		}
		if _, err := w.w.WriteString(")"); err != nil {
			return err
		}
	case DelimKind:
		if !isValidDelimiter(tok.Value) {
			return fmt.Errorf("write token: invalid delimiter %q", tok.Value)
		}
		if _, err := w.w.WriteString(tok.Value); err != nil {
			return err
		}
		if tok.Value == `\` {
			if _, err := w.w.WriteString("\n"); err != nil {
				return err
			}
		}
	case NumberKind:
		if !isValidNumber(tok.Value) {
			return fmt.Errorf("write token: invalid number %q", tok.Value)
		}
		_, err := w.w.WriteString(tok.Value)
		return err
	case PercentageKind:
		if !isValidNumber(tok.Value) {
			return fmt.Errorf("write token: invalid number %q", tok.Value)
		}
		if _, err := w.w.WriteString(tok.Value); err != nil {
			return err
		}
		if _, err := w.w.WriteString("%"); err != nil {
			return err
		}
		return nil
	case DimensionKind:
		if !isValidNumber(tok.Value) {
			return fmt.Errorf("write token: invalid number %q", tok.Value)
		}
		if _, err := w.w.WriteString(tok.Value); err != nil {
			return err
		}
		if len(tok.Unit) >= 2 &&
			(tok.Unit[0] == 'e' || tok.Unit[0] == 'E') &&
			(tok.Unit[1] == '-' || isDigit(rune(tok.Unit[1]))) {
			// Special case: unit could be parsed as an exponent.
			// Prepend backslash to escape the "e", which forces identifier parsing.
			if _, err := w.w.WriteString(`\`); err != nil {
				return err
			}
		}
		if err := writeIdent(w.w, tok.Unit); err != nil {
			return err
		}
	default:
		return fmt.Errorf("write token: %v not supported", err)
	}

	return nil
}

func writeIdent(w stringWriter, s string) error {
	if len(s) == 0 {
		return errors.New("write token: empty identifier")
	}

	// Fast path: if we don't need any escapes, pass through s.
	// isIdentStart and isIdent return true for any non-ASCII character,
	// so we can operate on bytes without decoding UTF-8.
	switch {
	case isIdentStart(rune(s[0])) || s[0] == '-' && len(s) > 2 && (isIdentStart(rune(s[1])) || s[1] == '-'):
		needsEscape := false
		start := 1
		if s[0] == '-' {
			start = 2
		}
		for _, b := range []byte(s[start:]) {
			if !isIdent(rune(b)) {
				needsEscape = true
				break
			}
		}
		if !needsEscape {
			_, err := w.WriteString(s)
			return err
		}
	case s == "-":
		_, err := w.WriteString(`\-`)
		return err
	case s == "--":
		_, err := w.WriteString(`\-\-`)
		return err
	}

	// Slow path: we need escapes.
	// Write out the first code point or two, since that's different from rest of string.
	if r0, size0 := utf8.DecodeRuneInString(s); isIdentStart(r0) {
		if _, err := w.WriteString(s[:size0]); err != nil {
			return err
		}
		s = s[size0:]
	} else if r0 == '-' {
		r1, size1 := utf8.DecodeRuneInString(s[size0:])
		if isIdentStart(r1) || r1 == '-' {
			if _, err := w.WriteString(s[:size0+size1]); err != nil {
				return err
			}
			s = s[size0+size1:]
		} else {
			escapeSeq := appendEscape(make([]byte, 0, maxEscapeLength), r0)
			if _, err := w.Write(escapeSeq); err != nil {
				return err
			}
			s = s[size0:]
		}
	} else {
		escapeSeq := appendEscape(make([]byte, 0, maxEscapeLength), r0)
		if _, err := w.Write(escapeSeq); err != nil {
			return err
		}
		s = s[size0:]
	}
	// Now the rest of the string can be escaped uniformly.
	return escape(w, s, func(r rune) bool { return !isIdent(r) })
}

// escape writes s to w, escaping every rune for which needsEscape reports true
// with [appendEscape].
func escape(w stringWriter, s string, needsEscape func(rune) bool) error {
	buf := make([]byte, 0, maxEscapeLength)
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if size == 0 {
			break
		}
		if needsEscape(r) {
			if _, err := w.WriteString(s[:i]); err != nil {
				return err
			}
			buf = appendEscape(buf[:0], r)
			if _, err := w.Write(buf); err != nil {
				return err
			}
			s = s[i+size:]
			i = 0
		} else {
			i += size
		}
	}
	if len(s) == 0 {
		return nil
	}
	_, err := w.WriteString(s)
	return err
}

// maxEscapeLength is the maximum number of bytes appended by [appendEscape].
const maxEscapeLength = len(`\`) + max(utf8.UTFMax, len("ff "))

// appendEscape appends the escape for the given code point to w.
func appendEscape(dst []byte, r rune) []byte {
	const hexDigits = "0123456789abcdef"
	dst = append(dst, '\\')
	if _, isHex := hexDigit(r); !isHex && r >= '!' && r != 0x7f {
		// Printable and not a hex digit. Can use directly.
		return utf8.AppendRune(dst, r)
	}
	start := len(dst)
	for {
		dst = append(dst, hexDigits[r&0xf])
		r >>= 4
		if r == 0 {
			break
		}
	}
	slices.Reverse(dst[start:])
	dst = append(dst, ' ')
	return dst
}

func requiresCommentSeparator(t1, t2 Token) bool {
	switch t1.Kind {
	case IdentKind:
		return t2.Kind == IdentKind ||
			t2.Kind == FunctionKind ||
			t2.Kind == URLKind ||
			t2.Kind == BadURLKind ||
			t2.IsDelim('-') ||
			t2.Kind == NumberKind ||
			t2.Kind == PercentageKind ||
			t2.Kind == DimensionKind ||
			t2.Kind == CDCKind ||
			t2.Kind == LParenKind
	case AtKeywordKind, HashKind, DimensionKind:
		return t2.Kind == IdentKind ||
			t2.Kind == FunctionKind ||
			t2.Kind == URLKind ||
			t2.Kind == BadURLKind ||
			t2.IsDelim('-') ||
			t2.Kind == NumberKind ||
			t2.Kind == PercentageKind ||
			t2.Kind == DimensionKind ||
			t2.Kind == CDCKind
	case DelimKind:
		switch t1.Value {
		case "#", "-":
			return t2.Kind == IdentKind ||
				t2.Kind == FunctionKind ||
				t2.Kind == URLKind ||
				t2.Kind == BadURLKind ||
				t2.IsDelim('-') ||
				t2.Kind == NumberKind ||
				t2.Kind == PercentageKind ||
				t2.Kind == DimensionKind ||
				t2.Kind == CDCKind
		case "@":
			return t2.Kind == IdentKind ||
				t2.Kind == FunctionKind ||
				t2.Kind == URLKind ||
				t2.Kind == BadURLKind ||
				t2.IsDelim('-') ||
				t2.Kind == CDCKind
		case ".", "+":
			return t2.Kind == NumberKind || t2.Kind == PercentageKind || t2.Kind == DimensionKind
		case "/":
			return t2.IsDelim('*')
		}
	}
	return false
}

type stringWriter interface {
	io.Writer
	io.StringWriter
}

type fallbackStringWriter struct {
	io.Writer
}

func (fsw fallbackStringWriter) WriteString(s string) (int, error) {
	return fsw.Write([]byte(s))
}
