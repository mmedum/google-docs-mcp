package main

import "testing"

// mustModuleRoot is moduleRoot for a test, which wants a fatal rather
// than an error. Four copies of this walk existed as test helpers before
// the gates shared one.
func mustModuleRoot(t *testing.T) string {
	t.Helper()
	root, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	return root
}
