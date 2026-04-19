package css

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// A Writer writes CSS tokens to an [io.Writer].
type Writer struct {
	w         stringWriter
	prevKind  Kind
	prevValue string
	newline   []byte
}

// NewWriter returns a new [*Writer] that writes to w.
func NewWriter(w io.Writer) *Writer {
	sw, ok := w.(stringWriter)
	if !ok {
		sw = fallbackStringWriter{w}
	}
	return &Writer{
		w:       sw,
		newline: append(make([]byte, 0, 8), '\n'),
	}
}

// WriteToken writes the token to the underlying writer.
func (w *Writer) WriteToken(tok Token) (err error) {
	if tok.Kind == WhitespaceKind {
		switch w.prevKind {
		case WhitespaceKind:
			return nil
		case SemicolonKind:
			// No indentation change.
		case LBraceKind:
			w.newline = append(w.newline, '\t')
		case RBraceKind:
			w.newline = w.newline[:max(len(w.newline)-1, 1)]
		default:
			w.prevKind = WhitespaceKind
			w.prevValue = ""
			_, err := w.w.WriteString(" ")
			return err
		}
		w.prevKind = WhitespaceKind
		w.prevValue = ""
		_, err := w.w.Write(w.newline)
		return err
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
		// TODO(maybe): Any other escapes?
		if _, err := w.w.WriteString(strings.ReplaceAll(tok.Value, `"`, `\"`)); err != nil {
			return err
		}
		if _, err := w.w.WriteString(`"`); err != nil {
			return err
		}
	case URLKind, BadURLKind:
		if _, err := w.w.WriteString("url("); err != nil {
			return err
		}
		// TODO(soon): Escape.
		if _, err := w.w.WriteString(tok.Value); err != nil {
			return err
		}
		if _, err := w.w.WriteString(")"); err != nil {
			return err
		}
	case DelimKind:
		if _, err := w.w.WriteString(tok.Value); err != nil {
			return err
		}
		if tok.Value == `\` {
			if _, err := w.w.WriteString("\n"); err != nil {
				return err
			}
		}
	case NumberKind:
		_, err := w.w.WriteString(tok.Value)
		return err
	case PercentageKind:
		if _, err := w.w.WriteString(tok.Value); err != nil {
			return err
		}
		if _, err := w.w.WriteString("%"); err != nil {
			return err
		}
		return nil
	case DimensionKind:
		if _, err := w.w.WriteString(tok.Value); err != nil {
			return err
		}
		// TODO(soon): Escape.
		if _, err := w.w.WriteString(tok.Unit); err != nil {
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

	// Needs some escapes. Get the start out of the way.
	switch {
	case isIdentStart(rune(s[0])):
		if _, err := w.WriteString(s[:1]); err != nil {
			return err
		}
		s = s[1:]
	case len(s) > 2 && s[0] == '-' && (isIdentStart(rune(s[1])) || s[1] == '-'):
		if _, err := w.WriteString(s[:2]); err != nil {
			return err
		}
		s = s[2:]
	default:
		if err := writeEscape(w, s[0]); err != nil {
			return err
		}
		s = s[1:]
	}

	// Write non-start characters out of the way.
	// Try to minimize the number of write calls.
	last := 0
	for i, b := range []byte(s) {
		if !isIdent(rune(b)) {
			if _, err := w.WriteString(s[last:i]); err != nil {
				return err
			}
			if err := writeEscape(w, b); err != nil {
				return err
			}
			last = i + 1
		}
	}
	_, err := w.WriteString(s[last:])
	return err
}

func writeEscape(w io.Writer, b byte) error {
	if isNonPrintable(rune(b)) {
		_, err := w.Write([]byte{'\\', b})
		return err
	}
	const hexDigits = "0123456789abcdef"
	_, err := w.Write([]byte{'\\', hexDigits[b>>4], hexDigits[b&0xf], ' '})
	return err
}

func requiresCommentSeparator(t1, t2 Token) bool {
	switch t1.Kind {
	case IdentKind:
		return t2.Kind == IdentKind ||
			t2.Kind == FunctionKind ||
			t2.Kind == URLKind ||
			t2.Kind == BadURLKind ||
			t2.Kind == DelimKind && t2.Value == "-" ||
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
			t2.Kind == DelimKind && t2.Value == "-" ||
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
				t2.Kind == DelimKind && t2.Value == "-" ||
				t2.Kind == NumberKind ||
				t2.Kind == PercentageKind ||
				t2.Kind == DimensionKind ||
				t2.Kind == CDCKind
		case "@":
			return t2.Kind == IdentKind ||
				t2.Kind == FunctionKind ||
				t2.Kind == URLKind ||
				t2.Kind == BadURLKind ||
				t2.Kind == DelimKind && t2.Value == "-" ||
				t2.Kind == CDCKind
		case ".", "+":
			return t2.Kind == NumberKind || t2.Kind == PercentageKind || t2.Kind == DimensionKind
		case "/":
			return t2.Kind == DelimKind && t2.Value == "*"
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
