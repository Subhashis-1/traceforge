// Package main provides a CQL migration tool for Trace Forge.
// It applies all .cql files from a specified directory to a Cassandra cluster.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gocql/gocql"
)

func main() {
	// Parse command-line flags
	host := flag.String("cassandra-host", "127.0.0.1", "Cassandra host address")
	keyspace := flag.String("keyspace", "traceforge", "Keyspace to migrate")
	cqlDir := flag.String("cql-dir", "../internal/storage", "Directory containing .cql migration files")
	flag.Parse()

	// Create Cassandra cluster configuration
	cluster := gocql.NewCluster(*host)
	cluster.Consistency = gocql.Quorum
	cluster.Keyspace = "system" // Start with system keyspace to create/check keyspace

	// Create session
	session, err := cluster.CreateSession()
	if err != nil {
		log.Fatalf("Failed to create Cassandra session: %v", err)
	}
	defer session.Close()

	fmt.Printf("Connected to Cassandra at %s\n", *host)

	// Ensure keyspace exists
	if err := createKeyspaceIfNotExists(session, *keyspace); err != nil {
		log.Fatalf("Failed to create keyspace: %v", err)
	}

	// Switch to the target keyspace
	cluster.Keyspace = *keyspace
	session, err = cluster.CreateSession()
	if err != nil {
		log.Fatalf("Failed to create session for keyspace %s: %v", *keyspace, err)
	}
	defer session.Close()

	fmt.Printf("Using keyspace: %s\n", *keyspace)

	// Find all .cql files in the directory
	cqlFiles, err := findCQLFiles(*cqlDir)
	if err != nil {
		log.Fatalf("Failed to find CQL files: %v", err)
	}

	if len(cqlFiles) == 0 {
		log.Fatalf("No .cql files found in %s", *cqlDir)
	}

	fmt.Printf("Found %d migration file(s)\n", len(cqlFiles))

	// Apply migrations
	for _, file := range cqlFiles {
		fmt.Printf("Applying migration: %s\n", file)

		content, err := os.ReadFile(file)
		if err != nil {
			log.Fatalf("Failed to read file %s: %v", file, err)
		}

		// Split file into individual statements (by semicolon)
		statements := splitCQLStatements(string(content))

		for _, stmt := range statements {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" || strings.HasPrefix(stmt, "--") {
				continue
			}

			// Skip USE statements - keyspace is already set in session
			if strings.HasPrefix(strings.ToUpper(stmt), "USE ") {
				continue
			}

			if err := session.Query(stmt).Exec(); err != nil {
				if isIgnorableSchemaError(stmt, err) {
					continue
				}
				log.Fatalf("Failed to execute statement from %s: %v\nStatement: %s", file, err, stmt)
			}
		}

		fmt.Printf("  ✓ %s completed\n", file)
	}

	fmt.Println("\n✅ Migrations applied successfully")
}

// createKeyspaceIfNotExists creates the keyspace if it doesn't exist
func createKeyspaceIfNotExists(session *gocql.Session, keyspace string) error {
	query := fmt.Sprintf(
		"CREATE KEYSPACE IF NOT EXISTS %s WITH replication = {'class': 'SimpleStrategy', 'replication_factor': '1'} AND durable_writes = true",
		keyspace,
	)
	return session.Query(query).Exec()
}

// findCQLFiles finds all .cql files in the given directory and returns them sorted alphabetically
func findCQLFiles(dir string) ([]string, error) {
	var files []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() && strings.HasSuffix(strings.ToLower(info.Name()), ".cql") {
			files = append(files, path)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Sort alphabetically to ensure deterministic migration order
	sort.Strings(files)

	return files, nil
}

// splitCQLStatements splits CQL content into individual statements by semicolon
// while respecting semicolons inside strings and comments
func splitCQLStatements(content string) []string {
	var statements []string
	var current strings.Builder
	inString := false
	stringChar := byte(0)
	inLineComment := false

	for i := 0; i < len(content); i++ {
		char := content[i]

		// Handle line comments
		if char == '-' && i+1 < len(content) && content[i+1] == '-' {
			inLineComment = true
			current.WriteByte(char)
			continue
		}

		if inLineComment && char == '\n' {
			inLineComment = false
			current.WriteByte(char)
			continue
		}

		if inLineComment {
			current.WriteByte(char)
			continue
		}

		// Handle string literals
		if char == '\'' || char == '"' {
			if !inString {
				inString = true
				stringChar = char
			} else if char == stringChar {
				inString = false
				stringChar = 0
			}
			current.WriteByte(char)
			continue
		}

		// Handle semicolon as statement delimiter
		if char == ';' && !inString {
			statements = append(statements, current.String())
			current.Reset()
			continue
		}

		current.WriteByte(char)
	}

	// Add any remaining content
	if remaining := strings.TrimSpace(current.String()); remaining != "" {
		statements = append(statements, remaining)
	}

	return statements
}

func isIgnorableSchemaError(stmt string, err error) bool {
	stmt = strings.ToUpper(strings.TrimSpace(stmt))
	if strings.HasPrefix(stmt, "ALTER TABLE ") && strings.Contains(stmt, " ADD ") {
		errMsg := strings.ToLower(err.Error())
		return strings.Contains(errMsg, "conflicts with an existing column") ||
			strings.Contains(errMsg, "duplicate column") ||
			strings.Contains(errMsg, "invalid column name") ||
			strings.Contains(errMsg, "already exists")
	}

	return false
}
