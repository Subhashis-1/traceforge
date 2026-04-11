// Package storage provides the data access layer for Trace Forge.
// It abstracts all Cassandra reads and writes behind a clean interface.
package storage

import "errors"

// Common errors returned by the Repository interface.
var (
	// ErrNotFound is returned when a requested entity does not exist.
	ErrNotFound = errors.New("entity not found")

	// ErrAlreadyExists is returned when attempting to create a duplicate entity.
	ErrAlreadyExists = errors.New("entity already exists")

	// ErrInvalidInput is returned when input parameters are invalid.
	ErrInvalidInput = errors.New("invalid input parameters")

	// ErrConnectionFailed is returned when Cassandra connection fails.
	ErrConnectionFailed = errors.New("failed to connect to Cassandra")
)
