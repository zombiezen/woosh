package woosh

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// A URLOpener can read the contents of a URL.
// The OpenURL method must be safe to call from multiple goroutines concurrently.
type URLOpener interface {
	OpenURL(u *url.URL) (io.ReadCloser, error)
}

// DefaultURLOpener returns a [URLOpener] that handles file:// and woosh:// URLs.
func DefaultURLOpener() URLOpener {
	return URLOpenerMux{
		"file":  FileURLOpener(),
		"woosh": BuiltinURLOpener(),
	}
}

type fileURLOpener struct{}

// FileURLOpener returns a [URLOpener] that handles file:// URLs.
func FileURLOpener() URLOpener {
	return fileURLOpener{}
}

func (fileURLOpener) OpenURL(u *url.URL) (io.ReadCloser, error) {
	if u.Scheme != "file" || u.Host != "" && u.Host != "localhost" {
		return nil, fmt.Errorf("%v not supported", u)
	}
	return os.Open(filepath.FromSlash(u.Path))
}

//go:embed theme.css
//go:embed preflight.css
//go:embed utilities.css
//go:embed woosh.css
var builtinStylesheets embed.FS

type fsURLOpener struct {
	scheme string
	fs     fs.FS
}

// BuiltinURLOpener returns a [URLOpener] that handles woosh:// URLs.
func BuiltinURLOpener() URLOpener {
	return &fsURLOpener{
		scheme: "woosh",
		fs:     builtinStylesheets,
	}
}

func (o *fsURLOpener) OpenURL(u *url.URL) (io.ReadCloser, error) {
	if u.Scheme != o.scheme || u.Host != "" {
		return nil, fmt.Errorf("%v not supported", u)
	}
	f, err := o.fs.Open(strings.TrimPrefix(path.Clean(u.Path), "/"))
	if err != nil {
		return nil, fmt.Errorf("open %v: %v", u, err)
	}
	if info, err := f.Stat(); err != nil {
		f.Close()
		return nil, fmt.Errorf("open %v: %v", u, err)
	} else if mode := info.Mode(); !mode.IsRegular() {
		f.Close()
		return nil, fmt.Errorf("open %v: not a regular file", u)
	}
	return f, nil
}

// URLOpenerMux is a [URLOpener] that uses other [URLOpener] objects
// based on the URL scheme.
type URLOpenerMux map[string]URLOpener

// OpenURL calls OpenURL on the corresponding [URLOpener] for the URL's scheme,
// or returns an error if the URL's scheme does not have a corresponding [URLOpener].
func (mux URLOpenerMux) OpenURL(u *url.URL) (io.ReadCloser, error) {
	o := mux[u.Scheme]
	if o == nil {
		return nil, fmt.Errorf("%v not supported", u)
	}
	return o.OpenURL(u)
}
