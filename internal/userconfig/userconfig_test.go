package userconfig

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPathsAndRoundTrip(t *testing.T) {
	base := t.TempDir()
	t.Setenv(EnvDir, base)

	if d, _ := ProfileDir(""); d != base {
		t.Fatalf("default profile dir = %s", d)
	}
	if d, _ := ProfileDir("work"); d != filepath.Join(base, "profiles", "work") {
		t.Fatalf("named profile dir = %s", d)
	}
	if p, _ := DefaultClientSecretPath("default"); p != filepath.Join(base, "client_secret.json") {
		t.Fatalf("client secret path = %s", p)
	}
	if p, _ := TokenFilePath("work"); p != filepath.Join(base, "profiles", "work", "token.json") {
		t.Fatalf("token path = %s", p)
	}

	if _, err := Load("work"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load before Save = %v", err)
	}
	in := Config{ClientSecretPath: "/x/secret.json", AccountEmail: "a@b.test", TokenStore: "keyring", Scopes: []string{"s1"}}
	if err := Save("work", in); err != nil {
		t.Fatal(err)
	}
	out, err := Load("work")
	if err != nil {
		t.Fatal(err)
	}
	if out.ClientSecretPath != in.ClientSecretPath || out.AccountEmail != in.AccountEmail || out.TokenStore != "keyring" || len(out.Scopes) != 1 || out.UpdatedAt.IsZero() {
		t.Fatalf("round trip lost data: %+v", out)
	}
	p, _ := Path("work")
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	// Windows has no POSIX permission bits, so the 0600 the writer asks
	// for cannot be asserted there; the file's ACL is what protects it.
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", st.Mode().Perm())
	}
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load("work"); err == nil {
		t.Fatal("corrupt file should error")
	}
	if err := Remove("work"); err != nil {
		t.Fatal(err)
	}
	if err := Remove("work"); err != nil {
		t.Fatalf("second remove should be fine: %v", err)
	}
}

func TestBaseDirFallsBackToUserConfigDir(t *testing.T) {
	t.Setenv(EnvDir, "")
	d, err := BaseDir()
	if err != nil {
		t.Skip("no user config dir on this system")
	}
	if filepath.Base(d) != AppDir {
		t.Fatalf("base dir = %s", d)
	}
}
