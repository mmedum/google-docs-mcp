package main

import (
	"io"
	"strings"
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

// Both ways the server builds a classed error are read: a class that
// reached the model only through an Error literal went undocumented.
func TestErrorfClassReadsBothForms(t *testing.T) {
	src := `return Errorf("invalid", "x")
	return &Error{Class: "blocked", Message: m}
	return &Error{Class:"unsupported"}`
	var got []string
	for _, m := range errorfClass.FindAllStringSubmatch(src, -1) {
		got = append(got, m[1])
	}
	if strings.Join(got, ",") != "invalid,blocked,unsupported" {
		t.Errorf("found %v", got)
	}
}
