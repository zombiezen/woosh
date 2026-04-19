package css

import (
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// FuzzWriter verifies that tokens can be round-tripped through the scanner.
func FuzzWriter(f *testing.F) {
	for _, test := range scannerTests {
		f.Add(test.source)
	}

	f.Fuzz(func(t *testing.T, css string) {
		s := NewScanner(strings.NewReader(css))
		var tokens1 []Token
		for {
			tok, err := s.Next()
			if tok.Kind == EOFKind {
				if err != io.EOF {
					t.Skip("At EOF:", err)
				}
				break
			}
			if err != nil {
				t.Skipf("Error on token %d: %v", len(tokens1), err)
			}
			tokens1 = append(tokens1, tok)
		}

		buf := new(bytes.Buffer)
		w := NewWriter(buf)
		for i, tok := range tokens1 {
			if err := w.WriteToken(tok); err != nil {
				t.Fatalf("Write tokens[%d] (token=%v): %v", i, tok, err)
			}
		}
		t.Logf("Original:\n%s", css)
		t.Logf("Rewritten:\n%s", buf)

		s = NewScanner(buf)
		var tokens2 []Token
		for {
			tok, err := s.Next()
			if tok.Kind == EOFKind {
				if err != io.EOF {
					t.Error("At EOF:", err)
				}
				break
			}
			if err != nil {
				t.Errorf("Error on token %d: %v", len(tokens2), err)
			}
			tokens2 = append(tokens2, tok)
		}
		ignoreLocation := cmp.FilterPath(func(p cmp.Path) bool {
			return p.Index(-2).Type() == reflect.TypeFor[Token]() &&
				p.Last().(cmp.StructField).Name() == "Start"
		}, cmp.Ignore())
		if diff := cmp.Diff(tokens1, tokens2, cmpopts.EquateEmpty(), ignoreLocation); diff != "" {
			t.Errorf("-want +got:\n%s", diff)
		}
	})
}
