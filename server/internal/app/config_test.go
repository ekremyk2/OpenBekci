package app

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goodKey = "Zk3vQ8mTn1Lp6RyW0aSd9XcHj4BfEu7GoIt2NhVwYb5KxMq+CrAe/DlJ8PgUs1ZO"

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func baseEnv() map[string]string {
	return map[string]string{
		"OPENBEKCI_DATABASE_URL":   "postgres://openbekci_app:pw@db:5432/openbekci",
		"OPENBEKCI_BASE_URL":       "https://bekci.example.org/",
		"OPENBEKCI_SECRET_KEY":     goodKey,
		"OPENBEKCI_ENCRYPTION_KEY": strings.ToUpper(goodKey[:20]) + goodKey[20:] + "a",
	}
}

func TestLoadConfigDefaultsAndDerivedURLs(t *testing.T) {
	c, err := LoadConfig("", env(baseEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != ":8080" || c.DataDir != "data" || !c.Worker {
		t.Errorf("defaults wrong: %+v", c)
	}
	if c.BaseURL != "https://bekci.example.org" {
		t.Errorf("BaseURL = %q", c.BaseURL)
	}
	if want := "postgres://openbekci_platform:pw@db:5432/openbekci"; c.PlatformDatabaseURL != want {
		t.Errorf("PlatformDatabaseURL = %q, want %q", c.PlatformDatabaseURL, want)
	}
	if want := "postgres://openbekci_migrator:pw@db:5432/openbekci"; c.MigratorDatabaseURL != want {
		t.Errorf("MigratorDatabaseURL = %q, want %q", c.MigratorDatabaseURL, want)
	}
	if err := c.ValidateServe(); err != nil {
		t.Errorf("ValidateServe: %v", err)
	}
}

func TestLoadConfigFileVariants(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "secret")
	if err := os.WriteFile(keyFile, []byte(goodKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := baseEnv()
	delete(e, "OPENBEKCI_SECRET_KEY")
	e["OPENBEKCI_SECRET_KEY_FILE"] = keyFile
	c, err := LoadConfig("", env(e))
	if err != nil {
		t.Fatal(err)
	}
	if string(c.SecretKey) != goodKey {
		t.Errorf("SecretKey from _FILE = %q", c.SecretKey)
	}

	e["OPENBEKCI_SECRET_KEY_FILE"] = filepath.Join(dir, "missing")
	if _, err := LoadConfig("", env(e)); err == nil {
		t.Error("expected error for unreadable _FILE")
	}
}

func TestLoadConfigYAMLUnderEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.yaml")
	yml := "listen_addr: \":9999\"\nbase_url: https://yaml.example.org\nextra_origins:\n  - https://a.example.org\n  - https://b.example.org\nworker: off\n"
	if err := os.WriteFile(path, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	e := baseEnv() // env sets BASE_URL, which must win
	c, err := LoadConfig(path, env(e))
	if err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != ":9999" || c.Worker {
		t.Errorf("yaml values not applied: %+v", c)
	}
	if c.BaseURL != "https://bekci.example.org" {
		t.Errorf("env should override yaml, BaseURL = %q", c.BaseURL)
	}
	if len(c.ExtraOrigins) != 2 {
		t.Errorf("ExtraOrigins = %v", c.ExtraOrigins)
	}
}

func TestLoadConfigBadValues(t *testing.T) {
	for _, tc := range []struct{ k, v string }{
		{"OPENBEKCI_WORKER", "sometimes"},
		{"OPENBEKCI_DEBUG", "maybe"},
		{"OPENBEKCI_TRUSTED_PROXIES", "not-an-ip"},
	} {
		e := baseEnv()
		e[tc.k] = tc.v
		if _, err := LoadConfig("", env(e)); err == nil {
			t.Errorf("%s=%s: expected error", tc.k, tc.v)
		}
	}
}

func TestCheckKey(t *testing.T) {
	cases := []struct {
		name string
		key  string
		dev  bool
		ok   bool
	}{
		{"random base64", goodKey, false, true},
		{"random hex", "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08", false, true},
		{"empty", "", false, false},
		{"too short", "abcdefghijklmnopqrstuvwxyz", false, false},
		{"placeholder", "changeme-changeme-changeme-changeme-changeme", false, false},
		{"placeholder mixed case", "ChangeMe-" + goodKey, false, false},
		{"repeated char", strings.Repeat("a", 64), false, false},
		{"repeated pattern", strings.Repeat("ab", 40), false, false},
		{"dev key refused in prod", "O5UXjIbaHRkxmT+4T/2TwQBBJYnTTVA7+x3jgVKFi9A3P/dRrnmw6gmCVcs5LkA/", false, false},
		{"dev key allowed in dev", "O5UXjIbaHRkxmT+4T/2TwQBBJYnTTVA7+x3jgVKFi9A3P/dRrnmw6gmCVcs5LkA/", true, true},
	}
	for _, tc := range cases {
		err := CheckKey("SECRET_KEY", []byte(tc.key), tc.dev)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

func TestValidateServeBaseURL(t *testing.T) {
	cases := []struct {
		url string
		dev bool
		ok  bool
	}{
		{"https://x.example.org", false, true},
		{"http://x.example.org", true, false},
		{"http://localhost:8484", false, false},
		{"http://localhost:8484", true, true},
		{"not a url", false, false},
		{"", false, false},
	}
	for _, tc := range cases {
		e := baseEnv()
		e["OPENBEKCI_BASE_URL"] = tc.url
		if tc.dev {
			e["OPENBEKCI_INSECURE_DEV"] = "true"
		}
		c, err := LoadConfig("", env(e))
		if err != nil {
			t.Fatal(err)
		}
		if err := c.ValidateServe(); (err == nil) != tc.ok {
			t.Errorf("BASE_URL %q dev=%v: err = %v, want ok=%v", tc.url, tc.dev, err, tc.ok)
		}
	}
}

func TestParseTrustedProxies(t *testing.T) {
	got, err := ParseTrustedProxies("10.0.0.0/8, 192.168.1.5 ,::1,fd00::/8")
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("192.168.1.5/32"),
		netip.MustParsePrefix("::1/128"),
		netip.MustParsePrefix("fd00::/8"),
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %v, want %v", i, got[i], want[i])
		}
	}
	if got, err := ParseTrustedProxies(""); err != nil || len(got) != 0 {
		t.Errorf("empty: %v, %v", got, err)
	}
	if _, err := ParseTrustedProxies("10.0.0.0/33"); err == nil {
		t.Error("expected error for bad CIDR")
	}
}

func TestCheckDataDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "data")
	if err := CheckDataDir(dir); err != nil {
		t.Fatalf("new dir: %v", err)
	}
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckDataDir(file); err == nil {
		t.Error("expected error when DATA_DIR is a file")
	}
}
