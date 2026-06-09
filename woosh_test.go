// Copyright 2026 Roxy Light
// SPDX-License-Identifier: MIT

package woosh

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"zombiezen.com/go/woosh/internal/css"
)

func TestProcess(t *testing.T) {
	list, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range list {
		testName := entry.Name()
		if strings.HasPrefix(testName, ".") || !entry.IsDir() {
			continue
		}
		t.Run(testName, func(t *testing.T) {
			inputURL, err := url.Parse("testdata:///input.css")
			if err != nil {
				t.Fatal(err)
			}
			dir, err := os.OpenRoot(filepath.Join("testdata", testName))
			if err != nil {
				t.Fatal(err)
			}
			defer dir.Close()

			buf := new(bytes.Buffer)
			err = Process(buf, &Options{
				Entrypoints: []*url.URL{inputURL},
				URLOpener:   testdataOpener{dir.FS()},
				Sources: func(yield func(io.ReadCloser) bool) {
					f, err := dir.Open("source.html")
					if err != nil {
						if !errors.Is(err, fs.ErrNotExist) {
							t.Error(err)
						}
					} else {
						yield(f)
					}
				},
			})
			if err != nil {
				t.Error("Process:", err)
			}

			wantCSS, err := dir.ReadFile("output.css")
			if err != nil {
				t.Fatal(err)
			}
			want, err := readTokens(bytes.NewReader(wantCSS))
			if err != nil {
				t.Errorf("testdata/%s/output.css: %v", testName, err)
			}
			got, err := readTokens(bytes.NewReader(buf.Bytes()))
			if err != nil {
				t.Errorf("invalid output: %v\n%s", err, buf.Bytes())
			}

			outputEquals := cmp.Equal(
				want, got,
				cmp.FilterPath(func(p cmp.Path) bool {
					if p.Index(-2).Type() != reflect.TypeFor[css.Token]() {
						return false
					}
					name := p.Last().(cmp.StructField).Name()
					return name == "Start"
				}, cmp.Ignore()),
			)
			if !outputEquals {
				t.Errorf("output:\n%s\nwant:\n%s", buf.Bytes(), wantCSS)
			}
		})
	}
}

func readTokens(r io.RuneReader) ([]css.Token, error) {
	s := css.NewScanner(r)
	var result []css.Token
	for {
		tok, err := s.Next()
		if tok.Kind == css.EOFKind {
			if err == io.EOF {
				err = nil
			}
			return result, err
		}
		result = append(result, tok)
		if err != nil {
			return result, err
		}
	}
}

type testdataOpener struct {
	fsys fs.FS
}

func (o testdataOpener) OpenURL(u *url.URL) (io.ReadCloser, error) {
	if u.Scheme != "testdata" {
		return nil, fmt.Errorf("unsupported scheme in %v", u)
	}
	return o.fsys.Open(strings.TrimPrefix(u.Path, "/"))
}
