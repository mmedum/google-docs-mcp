package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The vendored schemas are only worth their provenance: without the
// recorded digest, "make the document pass" and "edit the schema" are
// the same amount of work, and the second one is silent.
func TestAVendoredSchemaIsHeldToItsRecordedDigest(t *testing.T) {
	for name := range vendoredSchemas {
		if _, err := loadSchema(name); err != nil {
			t.Errorf("the committed %s does not match its recorded digest: %v", name, err)
		}
	}

	// And the check bites: a schema whose digest is not the recorded
	// one is refused rather than used.
	saved := vendoredSchemas["mcpb-manifest-v0.3.schema.json"]
	t.Cleanup(func() { vendoredSchemas["mcpb-manifest-v0.3.schema.json"] = saved })
	wrong := saved
	wrong.sha256 = strings.Repeat("0", 64)
	vendoredSchemas["mcpb-manifest-v0.3.schema.json"] = wrong

	_, err := loadSchema("mcpb-manifest-v0.3.schema.json")
	if err == nil {
		t.Fatal("a schema that does not match its recorded digest was used anyway")
	}
	if !strings.Contains(err.Error(), "recorded digest") {
		t.Errorf("the message does not say what is wrong: %v", err)
	}
}

// Every vendored file is recorded, and every record has a file. A
// schema in the directory that nothing names is a schema nothing
// checks, and a record naming a file that is not there fails only when
// something reaches for it.
func TestTheVendoredFilesAndTheRecordsAgree(t *testing.T) {
	entries, err := schemaFS.ReadDir("schemas")
	if err != nil {
		t.Fatal(err)
	}
	onDisk := map[string]bool{}
	for _, e := range entries {
		onDisk[e.Name()] = true
		if _, recorded := vendoredSchemas[e.Name()]; !recorded {
			t.Errorf("schemas/%s is vendored and has no recorded digest, so nothing holds it", e.Name())
		}
	}
	for name, v := range vendoredSchemas {
		if !onDisk[name] {
			t.Errorf("a digest is recorded for %s, which is not in schemas/", name)
		}
		if v.source == "" {
			t.Errorf("%s records no source, so a re-fetch has nowhere to read from", name)
		}
	}
	if len(entries) < 2 {
		t.Fatalf("found %d vendored schema(s); the registry entry and the bundle manifest are both held", len(entries))
	}
}

// The committed manifest satisfies the schema it cites — the claim the
// $schema rules make and cannot test.
func TestTheCommittedManifestSatisfiesItsSchema(t *testing.T) {
	t.Chdir("../..")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDocument(mcpbSchemaFile, "the committed manifest", raw); err != nil {
		t.Errorf("%v", err)
	}
}

// And the validation bites. The cases are what this schema ACTUALLY
// constrains, which is less than it sounds like: `version` is a string
// of at most 255 characters with no pattern, and a package's
// `registryType` is a bare string with no enum, so "not a version" and
// "tarball" are both valid documents. The rules in mcpregistry.go hold
// what the schema leaves open.
//
// The mutations are applied to the DOCUMENT rather than to a struct,
// because the interesting failure is a missing field and a Go struct
// without omitempty cannot express one.
func TestARegistryEntryThatBreaksTheSchemaIsRefused(t *testing.T) {
	entry := map[string]any{
		"$schema":     "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json",
		"name":        "io.github.example/example-mcp",
		"description": "An example server, for a document this test can hold without a release.",
		"version":     "1.2.3",
		"websiteUrl":  "https://github.com/example/example-mcp#readme",
		"repository":  map[string]any{"url": "https://github.com/example/example-mcp", "source": "github"},
		"packages": []any{map[string]any{
			"registryType": "mcpb",
			"identifier":   "https://github.com/example/example-mcp/releases/download/v1.2.3/example-mcp_1.2.3.mcpb",
			"fileSha256":   strings.Repeat("a", 64),
			"version":      "1.2.3",
			"transport":    map[string]any{"type": "stdio"},
		}},
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDocument(registrySchemaFile, "a registry entry", raw); err != nil {
		t.Fatalf("a well-formed entry was refused: %v", err)
	}

	cases := []struct {
		name   string
		breaks func(doc map[string]any)
	}{
		{"a name outside the namespace shape", func(d map[string]any) { d["name"] = "not a server name" }},
		{"a description past the registry's cap", func(d map[string]any) { d["description"] = strings.Repeat("x", 101) }},
		{"no version at all", func(d map[string]any) { delete(d, "version") }},
		{"a file hash that is not a sha256", func(d map[string]any) {
			d["packages"].([]any)[0].(map[string]any)["fileSha256"] = "nope"
		}},
		{"a package with no transport", func(d map[string]any) {
			delete(d["packages"].([]any)[0].(map[string]any), "transport")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			tc.breaks(doc)
			out, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateDocument(registrySchemaFile, "a registry entry", out); err == nil {
				t.Fatalf("%s was accepted, and an entry cannot be withdrawn", tc.name)
			}
		})
	}
}
