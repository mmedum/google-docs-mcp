package userconfig

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// The error paths: a config directory that cannot be located at all, a
// file that is not JSON, a save whose parent is a file, and a remove
// whose target is a non-empty directory. They are the branches a caller
// only meets on a broken machine, which is exactly when a bare
// "userconfig" in the message would waste someone's afternoon.
func TestErrorPaths(t *testing.T) {
	base := t.TempDir()
	t.Setenv(EnvDir, base)

	t.Run("unparsable config", func(t *testing.T) {
		p, err := Path("bad")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = Load("bad")
		if err == nil || errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "parse") {
			t.Fatalf("Load on a broken file = %v", err)
		}
	})

	t.Run("save under a file", func(t *testing.T) {
		// A regular file exactly where the profile's directory has to go:
		// MkdirAll cannot create it.
		blocker := filepath.Join(base, "profiles", "blocked")
		if err := os.MkdirAll(filepath.Dir(blocker), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(blocker, []byte("in the way"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(blocker) })
		if err := Save("blocked", Config{}); err == nil || !strings.Contains(err.Error(), "create") {
			t.Fatalf("Save into a blocked path = %v", err)
		}
	})

	t.Run("remove a directory", func(t *testing.T) {
		p, err := Path("dir")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(p, "child"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := Remove("dir"); err == nil || !strings.Contains(err.Error(), "remove") {
			t.Fatalf("Remove on a non-empty directory = %v", err)
		}
	})

	t.Run("missing file is not an error", func(t *testing.T) {
		if err := Remove("never-saved"); err != nil {
			t.Fatalf("Remove of an absent config = %v", err)
		}
	})
}

// With no environment override and no home directory, there is nowhere
// to put a config, and every path helper has to say so rather than
// returning a path relative to nothing.
func TestNoConfigDirectory(t *testing.T) {
	t.Setenv(EnvDir, "")
	if runtime.GOOS == "windows" {
		// os.UserConfigDir reads %AppData% on Windows and errors when it
		// is empty, which is the same experiment as clearing HOME
		// elsewhere. Skipping it here cost real coverage: the package sat
		// at 78.9% on the Windows runner and 93% everywhere else, and
		// nobody could see it while the floor ran on Linux alone.
		t.Setenv("AppData", "")
	} else {
		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("HOME", "")
	}
	for name, fn := range map[string]func(string) (string, error){
		"ProfileDir": ProfileDir, "Path": Path,
		"DefaultClientSecretPath": DefaultClientSecretPath, "TokenFilePath": TokenFilePath,
	} {
		if _, err := fn("default"); err == nil {
			t.Errorf("%s should fail with no config directory", name)
		}
	}
	if _, err := Load("default"); err == nil {
		t.Error("Load should fail with no config directory")
	}
	if err := Save("default", Config{}); err == nil {
		t.Error("Save should fail with no config directory")
	}
	if err := Remove("default"); err == nil {
		t.Error("Remove should fail with no config directory")
	}
}
