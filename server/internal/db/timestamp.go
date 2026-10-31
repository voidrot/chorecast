package db

import "time"

// timestampLayout matches the strftime('%Y-%m-%dT%H:%M:%fZ') defaults in the schema, so
// timestamps compare correctly as text.
const timestampLayout = "2006-01-02T15:04:05.000Z"

// Timestamp formats t for storage in a TEXT timestamp column.
func Timestamp(t time.Time) string { return t.UTC().Format(timestampLayout) }

// ParseTimestamp parses a stored TEXT timestamp.
func ParseTimestamp(s string) (time.Time, error) { return time.Parse(timestampLayout, s) }
