package postgres

import "errors"

var errUnsupported = errors.New("postgres store does not support this tool in the reviewed backend")

func unsupported() error { return errUnsupported }
