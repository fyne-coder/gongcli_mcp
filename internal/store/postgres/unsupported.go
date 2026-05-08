package postgres

import "errors"

var errUnsupported = errors.New("postgres store does not support this tool in the first vertical slice")

func unsupported() error { return errUnsupported }
