package woosh

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
)

// A URLOpener can read the contents of a URL.
// The OpenURL method must be safe to call from multiple goroutines concurrently.
type URLOpener interface {
	OpenURL(u *url.URL) (io.ReadCloser, error)
}

// DefaultURLOpener returns a [URLOpener] that handles file:// URLs.
func DefaultURLOpener() URLOpener {
	return URLOpenerMux{
		"file": FileURLOpener(),
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
