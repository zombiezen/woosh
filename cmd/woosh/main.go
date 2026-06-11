// Copyright 2026 Roxy Light
// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"io"
	"iter"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/alecthomas/kong"
	"zombiezen.com/go/woosh"
)

type command struct {
	EntrypointPaths []string `kong:"arg,name=file,required,help=Read CSS from files."`
	Sources         []string `kong:"name=source,placeholder=FILE,help=Detect class names in sources."`
	OutputPath      string   `kong:"name=output,short=o,default=-,placeholder=FILE,help=Write CSS to file."`
}

func (c *command) Run() error {
	entrypoints := make([]*url.URL, 0, len(c.EntrypointPaths))
	for _, src := range c.EntrypointPaths {
		var err error
		src, err := filepath.Abs(src)
		if err != nil {
			return err
		}
		entrypoints = append(entrypoints, &url.URL{
			Scheme: "file",
			Path:   filepath.ToSlash(src),
		})
	}

	outFile := os.Stdout
	if c.OutputPath != "" && c.OutputPath != "-" {
		var err error
		outFile, err = os.Create(c.OutputPath)
		if err != nil {
			return err
		}
	}

	err := woosh.Process(outFile, &woosh.Options{
		Entrypoints: entrypoints,
		Sources:     sources(slices.Values(c.Sources)),
	})
	if err != nil {
		outFile.Close()
		return err
	}

	if err := outFile.Close(); err != nil {
		return err
	}

	return nil
}

func sources(roots iter.Seq[string]) iter.Seq[io.ReadCloser] {
	return func(yield func(io.ReadCloser) bool) {
		done := errors.New("yield returned false")
		for root := range roots {
			err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				name := entry.Name()
				if path != root && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
					if entry.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				if entry.IsDir() {
					return nil
				}
				if f, err := os.Open(path); err == nil {
					if !yield(f) {
						return done
					}
				}
				return nil
			})
			if err == done {
				return
			}
		}
	}
}

func main() {
	f := new(command)
	k := kong.Must(f, kong.Name("woosh"))
	kc, err := k.Parse(os.Args[1:])
	if err != nil {
		k.FatalIfErrorf(err)
	}
	if err := kc.Run(); err != nil {
		k.FatalIfErrorf(err)
	}
}
