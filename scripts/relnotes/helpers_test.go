package main

import (
	"os"
	"strings"
	"testing"
)

func writeFile(path, body string) error { return os.WriteFile(path, []byte(body), 0o644) }

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }
