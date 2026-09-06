package main

import (
	"io"
	"testing"
)

// The gate itself, run against this repository.
func TestClassesGate(t *testing.T) {
	if err := classes(io.Discard, nil); err != nil {
		t.Errorf("classes gate: %v", err)
	}
}

// service.Classes is an unsorted literal, so a duplicate is almost never
// adjacent to the value it repeats — which is the whole of what the
// check used to be able to see.
func TestHasDuplicate(t *testing.T) {
	cases := []struct {
		name string
		xs   []string
		want bool
	}{
		{"none", []string{"auth", "forbidden", "not_found", "unknown"}, false},
		{"repeated next to itself", []string{"auth", "auth", "not_found"}, true},
		{"repeated further along", []string{"auth", "forbidden", "not_found", "auth"}, true},
		{"empty", nil, false},
		{"one", []string{"auth"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasDuplicate(c.xs); got != c.want {
				t.Errorf("hasDuplicate(%v) = %v, want %v", c.xs, got, c.want)
			}
		})
	}
}
