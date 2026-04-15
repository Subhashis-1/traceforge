// Package api provides HTTP handlers for the Traceforge API.
package api

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Query represents a parsed DSL query for searching traces.
type Query struct {
	Service    string
	StatusOp   string // one of "=", "!=", ">", ">=", "<", "<="
	StatusVal  int
	DurationOp string
	DurationMs int64
	Tags       map[string]string
}

// ParseDSL parses a search query string into a Query struct.
// The DSL supports the following tokens (whitespace-separated):
//   - service:<name> - filter by service name
//   - status:<op><int> - filter by status code (op: =, !=, >, >=, <, <=)
//   - duration:<op><int>ms - filter by duration in milliseconds
//   - tag:<key>=<value> - filter by tag (multiple allowed)
// All fields are optional; unknown tokens return an error.
func ParseDSL(input string) (*Query, error) {
	query := &Query{
		Tags: make(map[string]string),
	}

	// Tokenize by whitespace
	tokens := strings.Fields(input)

	// Compile regex patterns
	statusPattern := regexp.MustCompile(`^(=|!=|>=|<=|>|<)(\d+)$`)
	durationPattern := regexp.MustCompile(`^(=|!=|>=|<=|>|<)(\d+)ms$`)
	tagPattern := regexp.MustCompile(`^([^=]+)=(.*)$`)

	for _, token := range tokens {
		// Parse service:<name>
		if strings.HasPrefix(token, "service:") {
			query.Service = strings.TrimPrefix(token, "service:")
			if query.Service == "" {
				return nil, fmt.Errorf("invalid token: %s (service name required)", token)
			}
			continue
		}

		// Parse status:<op><int>
		if strings.HasPrefix(token, "status:") {
			value := strings.TrimPrefix(token, "status:")
			matches := statusPattern.FindStringSubmatch(value)
			if len(matches) != 3 {
				return nil, fmt.Errorf("invalid token: %s (expected status:<op><int>)", token)
			}
			query.StatusOp = matches[1]
			statusVal, err := strconv.Atoi(matches[2])
			if err != nil {
				return nil, fmt.Errorf("invalid token: %s (invalid status value)", token)
			}
			query.StatusVal = statusVal
			continue
		}

		// Parse duration:<op><int>ms
		if strings.HasPrefix(token, "duration:") {
			value := strings.TrimPrefix(token, "duration:")
			matches := durationPattern.FindStringSubmatch(value)
			if len(matches) != 3 {
				return nil, fmt.Errorf("invalid token: %s (expected duration:<op><int>ms)", token)
			}
			query.DurationOp = matches[1]
			durationVal, err := strconv.ParseInt(matches[2], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid token: %s (invalid duration value)", token)
			}
			query.DurationMs = durationVal
			continue
		}

		// Parse tag:<key>=<value>
		if strings.HasPrefix(token, "tag:") {
			value := strings.TrimPrefix(token, "tag:")
			matches := tagPattern.FindStringSubmatch(value)
			if len(matches) != 3 {
				return nil, fmt.Errorf("invalid token: %s (expected tag:<key>=<value>)", token)
			}
			key := matches[1]
			val := matches[2]
			if key == "" {
				return nil, fmt.Errorf("invalid token: %s (tag key required)", token)
			}
			query.Tags[key] = val
			continue
		}

		// Unknown token
		return nil, fmt.Errorf("invalid token: %s", token)
	}

	return query, nil
}
