package routing

import "errors"

var (
	ErrInvalidConfig  = errors.New("invalid routing config")
	ErrUnsupportedKey = errors.New("unsupported routing config value")
)
