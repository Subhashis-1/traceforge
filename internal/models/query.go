package models

// Query represents a parsed DSL query for searching traces.
type Query struct {
	Service    string
	StatusOp   string
	StatusVal  int
	DurationOp string
	DurationMs int64
	Tags       map[string]string
}
