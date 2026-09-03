// Package userconfig stores non-secret, per-profile settings that survive
// between runs: where the OAuth client JSON lives, which account was
// logged in, and where the refresh token was stored. It lives at
// os.UserConfigDir()/google-docs-mcp (override with GDOCS_CONFIG_DIR);
// a non-default profile lives under profiles/<name>/ below that.
package userconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// AppDir is the directory name under the user's config directory.
const AppDir = "google-docs-mcp"

// EnvDir overrides the base directory (tests, unusual setups).
const EnvDir = "GDOCS_CONFIG_DIR"

// DefaultProfile is the profile used when none is named.
const DefaultProfile = "default"

// ErrNotFound is returned when the profile has no config file yet.
var ErrNotFound = errors.New("userconfig: no config file for this profile; run `google-docs-mcp login`")

// Config is the stored, non-secret profile state.
type Config struct {
	ClientSecretPath string    `json:"client_secret_path,omitempty"`
	AccountEmail     string    `json:"account_email,omitempty"`
	TokenStore       string    `json:"token_store,omitempty"`
	Scopes           []string  `json:"scopes,omitempty"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// BaseDir returns the application config directory.
func BaseDir() (string, error) {
	if v := os.Getenv(EnvDir); v != "" {
		return v, nil
	}
	d, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("userconfig: locate user config dir: %w", err)
	}
	return filepath.Join(d, AppDir), nil
}

// ProfileDir returns the directory holding one profile's files.
func ProfileDir(profile string) (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	if profile == "" || profile == DefaultProfile {
		return base, nil
	}
	return filepath.Join(base, "profiles", profile), nil
}

// Path returns the config file path for the profile.
func Path(profile string) (string, error) {
	dir, err := ProfileDir(profile)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// DefaultClientSecretPath is where `login` looks for the OAuth client JSON
// when no path is given.
func DefaultClientSecretPath(profile string) (string, error) {
	dir, err := ProfileDir(profile)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "client_secret.json"), nil
}

// TokenFilePath is the plaintext fallback location for the refresh token.
func TokenFilePath(profile string) (string, error) {
	dir, err := ProfileDir(profile)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "token.json"), nil
}

// Load reads the profile's config. ErrNotFound if absent.
func Load(profile string) (Config, error) {
	p, err := Path(profile)
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, ErrNotFound
	}
	if err != nil {
		return Config{}, fmt.Errorf("userconfig: read %s: %w", p, err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("userconfig: parse %s: %w", p, err)
	}
	return c, nil
}

// Save writes the profile's config with owner-only permissions.
func Save(profile string, c Config) error {
	p, err := Path(profile)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("userconfig: create %s: %w", filepath.Dir(p), err)
	}
	c.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("userconfig: encode: %w", err)
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("userconfig: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, p); err != nil {
		return fmt.Errorf("userconfig: replace %s: %w", p, err)
	}
	return nil
}

// Remove deletes the profile's config file. Missing files are not an error.
func Remove(profile string) error {
	p, err := Path(profile)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("userconfig: remove %s: %w", p, err)
	}
	return nil
}
