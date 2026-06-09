package css

import (
	"cmp"
	"errors"

	"zombiezen.com/go/woosh/internal/multierror"
)

// ErrorLocation returns the file name and [Location] that a parse error occurred at.
func ErrorLocation(err error) (filename string, loc Location, ok bool) {
	for {
		switch x := err.(type) {
		case *locationError:
			return x.file, x.loc, true
		case interface{ Unwrap() error }:
			err = x.Unwrap()
		case interface{ Unwrap() []error }:
			errs := x.Unwrap()
			if len(errs) != 1 {
				return "", Location{}, false
			}
			err = errs[0]
		default:
			return "", Location{}, false
		}
	}
}

// ErrorWithLocation returns an error that wraps err with the given file name and [Location]
// if the error does not already have a location.
// If the file name is empty, then it can be filled in later with [AddFileToError].
func ErrorWithLocation(filename string, loc Location, err error) error {
	var allErrors multierror.Collector
	for err := range multierror.All(err) {
		if _, hasLocation := errors.AsType[*locationError](err); !hasLocation {
			err = &locationError{
				file: filename,
				loc:  loc,
				err:  err,
			}
		}
		allErrors.Add(err)
	}
	return allErrors.Error()
}

// AddFileToError sets the file name of any error created by [ErrorWithLocation]
// founding using a depth-first traversal of any Unwrap() methods.
func AddFileToError(filename string, err error) {
	if filename == "" {
		return
	}
	for {
		switch x := err.(type) {
		case *locationError:
			x.file = cmp.Or(x.file, filename)
			return
		case interface{ Unwrap() error }:
			err = x.Unwrap()
		case interface{ Unwrap() []error }:
			for _, err := range x.Unwrap() {
				AddFileToError(filename, err)
			}
		default:
			return
		}
	}
}

type locationError struct {
	file string
	loc  Location
	err  error
}

func (e *locationError) Error() string { return e.err.Error() }
func (e *locationError) Unwrap() error { return e.err }
