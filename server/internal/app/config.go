// Package app holds configuration, startup checks, wiring and lifecycle.
package app

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const envPrefix = "OPENBEKCI_"

// Config is the process configuration (backend plan, section 17).
type Config struct {
	DatabaseURL         string
	MigratorDatabaseURL string
	PlatformDatabaseURL string

	BaseURL      string
	ExtraOrigins []string
	ListenAddr   string

	SecretKey     []byte
	EncryptionKey []byte

	DataDir        string
	TrustedProxies []netip.Prefix

	// MaxBodyBytes caps JSON API request bodies (OPENBEKCI_MAX_BODY_BYTES);
	// zero means the httpapi default (1 MiB). File uploads get their own limit.
	MaxBodyBytes int64

	// Worker runs the job worker inside serve.
	Worker      bool
	AutoMigrate bool
	InsecureDev bool
	Debug       bool

	// ValidateResponses checks every API response against the spec (a dev aid).
	ValidateResponses bool

	// Instance identity and branding for the discovery document.
	InstanceName         string
	MinAppVersionAndroid string
	MinAppVersionIOS     string
	BrandingLogoURL      string
	BrandingPrimaryColor string

	// ServerVersion is the build version; set by the command, not by the environment.
	ServerVersion string
}

// secretNames are the variables that accept a _FILE variant.
var secretNames = map[string]bool{
	"DATABASE_URL": true, "MIGRATOR_DATABASE_URL": true, "PLATFORM_DATABASE_URL": true,
	"SECRET_KEY": true, "ENCRYPTION_KEY": true,
}

// devKeys are the throwaway keys shipped in the repository's .env.example. They
// are accepted only with INSECURE_DEV=true.
var devKeys = map[string]bool{
	"O5UXjIbaHRkxmT+4T/2TwQBBJYnTTVA7+x3jgVKFi9A3P/dRrnmw6gmCVcs5LkA/": true,
	"FRc8OaqwKVXLgzx/hDqj4CueiU8gG+sZE69/DZjU4+ilciEEcoihYrJhYpcl/Jqg": true,
}

// LoadConfig reads configuration from the optional YAML file at path and from
// the environment, which wins. getenv is usually os.Getenv.
func LoadConfig(path string, getenv func(string) string) (Config, error) {
	file := map[string]string{}
	if path != "" {
		b, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			return Config{}, fmt.Errorf("read config file: %w", err)
		}
		if file, err = parseYAML(b); err != nil {
			return Config{}, fmt.Errorf("parse config file %s: %w", path, err)
		}
	}
	l := lookup{getenv: getenv, file: file}

	var c Config
	var err error
	if c.DatabaseURL, err = l.get("DATABASE_URL"); err != nil {
		return Config{}, err
	}
	if c.MigratorDatabaseURL, err = l.get("MIGRATOR_DATABASE_URL"); err != nil {
		return Config{}, err
	}
	if c.PlatformDatabaseURL, err = l.get("PLATFORM_DATABASE_URL"); err != nil {
		return Config{}, err
	}
	if c.MigratorDatabaseURL == "" {
		c.MigratorDatabaseURL = withRole(c.DatabaseURL, "openbekci_migrator")
	}
	if c.PlatformDatabaseURL == "" {
		c.PlatformDatabaseURL = withRole(c.DatabaseURL, "openbekci_platform")
	}

	c.BaseURL = strings.TrimRight(l.must("BASE_URL"), "/")
	c.ExtraOrigins = splitList(l.must("EXTRA_ORIGINS"))
	c.ListenAddr = l.must("LISTEN_ADDR")
	if c.ListenAddr == "" {
		c.ListenAddr = ":8080"
	}
	c.DataDir = l.must("DATA_DIR")
	if c.DataDir == "" {
		c.DataDir = "data"
	}

	if c.InstanceName = l.must("INSTANCE_NAME"); c.InstanceName == "" {
		c.InstanceName = "OpenBekci"
	}
	if c.MinAppVersionAndroid = l.must("MIN_APP_VERSION_ANDROID"); c.MinAppVersionAndroid == "" {
		c.MinAppVersionAndroid = "1.0.0"
	}
	if c.MinAppVersionIOS = l.must("MIN_APP_VERSION_IOS"); c.MinAppVersionIOS == "" {
		c.MinAppVersionIOS = "1.0.0"
	}
	c.BrandingLogoURL = l.must("BRANDING_LOGO_URL")
	c.BrandingPrimaryColor = l.must("BRANDING_PRIMARY_COLOR")

	if c.TrustedProxies, err = ParseTrustedProxies(l.must("TRUSTED_PROXIES")); err != nil {
		return Config{}, err
	}

	if v := l.must("MAX_BODY_BYTES"); v != "" {
		n, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil || n < 1 {
			return Config{}, fmt.Errorf("%sMAX_BODY_BYTES: %q is not a positive integer", envPrefix, v)
		}
		c.MaxBodyBytes = n
	}

	switch w := strings.ToLower(l.must("WORKER")); w {
	case "", "embedded":
		c.Worker = true
	case "off":
		c.Worker = false
	default:
		return Config{}, fmt.Errorf("%sWORKER: %q is not one of embedded, off", envPrefix, w)
	}
	for name, dst := range map[string]*bool{
		"AUTO_MIGRATE": &c.AutoMigrate, "INSECURE_DEV": &c.InsecureDev, "DEBUG": &c.Debug,
		"API_VALIDATE_RESPONSES": &c.ValidateResponses,
	} {
		if *dst, err = parseBool(l.must(name)); err != nil {
			return Config{}, fmt.Errorf("%s%s: %w", envPrefix, name, err)
		}
	}

	// Keys are decoded and checked in ValidateServe so that commands that do not
	// need them (migrate) work without them.
	secret, err := l.get("SECRET_KEY")
	if err != nil {
		return Config{}, err
	}
	enc, err := l.get("ENCRYPTION_KEY")
	if err != nil {
		return Config{}, err
	}
	c.SecretKey, c.EncryptionKey = []byte(secret), []byte(enc)
	return c, nil
}

// ValidateMigrate checks what the migrate command needs.
func (c Config) ValidateMigrate() error {
	if c.MigratorDatabaseURL == "" {
		return fmt.Errorf("%sMIGRATOR_DATABASE_URL (or %sDATABASE_URL) is required", envPrefix, envPrefix)
	}
	return nil
}

// ValidateServe checks what serve and worker need: URLs, keys, base URL.
func (c Config) ValidateServe() error {
	var errs []error
	if c.DatabaseURL == "" {
		errs = append(errs, fmt.Errorf("%sDATABASE_URL is required", envPrefix))
	}
	if c.BaseURL == "" {
		errs = append(errs, fmt.Errorf("%sBASE_URL is required", envPrefix))
	} else if err := c.validateBaseURL(); err != nil {
		errs = append(errs, err)
	}
	if len(c.SecretKey) > 0 && strings.TrimSpace(string(c.SecretKey)) == strings.TrimSpace(string(c.EncryptionKey)) {
		errs = append(errs, fmt.Errorf("%sSECRET_KEY and %sENCRYPTION_KEY must be different keys", envPrefix, envPrefix))
	}
	for name, key := range map[string][]byte{"SECRET_KEY": c.SecretKey, "ENCRYPTION_KEY": c.EncryptionKey} {
		if err := CheckKey(name, key, c.InsecureDev); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (c Config) validateBaseURL() error {
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("%sBASE_URL %q is not an http(s) URL", envPrefix, c.BaseURL)
	}
	if u.Scheme == "http" {
		switch u.Hostname() {
		case "localhost", "127.0.0.1", "::1":
			if !c.InsecureDev {
				return fmt.Errorf("%sBASE_URL uses http; set %sINSECURE_DEV=true for local development", envPrefix, envPrefix)
			}
		default:
			return fmt.Errorf("%sBASE_URL must be https (http is allowed only for localhost with INSECURE_DEV)", envPrefix)
		}
	}
	return nil
}

// CheckKey verifies a key is at least 32 random bytes and not a placeholder. raw
// is the configured text; hex and base64 encodings are decoded first.
func CheckKey(name string, raw []byte, insecureDev bool) error {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return fmt.Errorf("%s%s is required", envPrefix, name)
	}
	if devKeys[text] && !insecureDev {
		return fmt.Errorf("%s%s is the development key from .env.example; generate one with `openssl rand -base64 48`", envPrefix, name)
	}
	lower := strings.ToLower(text)
	for _, p := range []string{"changeme", "change-me", "change_me", "placeholder", "replace-me", "replace_me", "your-secret", "example"} {
		if strings.Contains(lower, p) {
			return fmt.Errorf("%s%s looks like a placeholder; generate one with `openssl rand -base64 48`", envPrefix, name)
		}
	}
	key, encoding := decodeKey(text)
	if len(key) < 32 {
		if encoding == "raw" {
			return fmt.Errorf("%s%s is too short: %d bytes as a raw passphrase; it must be at least 32 bytes. Generate one with `openssl rand -base64 48`", envPrefix, name, len(key))
		}
		return fmt.Errorf("%s%s is too short: it was read as %s and decoded to %d bytes, and as a raw passphrase it has %d bytes; at least 32 bytes are needed either way (hex and base64 are decoded automatically, a raw passphrase of 32 or more bytes is also accepted). Generate one with `openssl rand -base64 48`", envPrefix, name, encoding, len(key), len(text))
	}
	if distinct(key) < 8 {
		return fmt.Errorf("%s%s is not random enough (repeated characters); generate one with `openssl rand -base64 48`", envPrefix, name)
	}
	return nil
}

// decodeKey returns the key bytes and how they were read. Text that is valid
// hex or base64 is decoded; if that yields fewer than 32 bytes but the text
// itself is at least 32 bytes, the text is used as a raw passphrase.
func decodeKey(s string) ([]byte, string) {
	var decoded []byte
	how := ""
	if b, err := hex.DecodeString(s); err == nil {
		decoded, how = b, "hex"
	} else {
		for _, e := range []struct {
			name string
			enc  *base64.Encoding
		}{{"base64", base64.StdEncoding}, {"base64", base64.RawStdEncoding}, {"base64url", base64.URLEncoding}, {"base64url", base64.RawURLEncoding}} {
			if b, err := e.enc.DecodeString(s); err == nil {
				decoded, how = b, e.name
				break
			}
		}
	}
	if how == "" || (len(decoded) < 32 && len(s) >= 32) {
		return []byte(s), "raw"
	}
	return decoded, how
}

func distinct(b []byte) int {
	var seen [256]bool
	n := 0
	for _, c := range b {
		if !seen[c] {
			seen[c] = true
			n++
		}
	}
	return n
}

// ParseTrustedProxies parses a comma-separated list of CIDRs or bare IPs.
func ParseTrustedProxies(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, item := range splitList(s) {
		if p, err := netip.ParsePrefix(item); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(item)
		if err != nil {
			return nil, fmt.Errorf("%sTRUSTED_PROXIES: %q is not a CIDR or IP address", envPrefix, item)
		}
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

// CheckDataDir verifies dir exists (creating it if needed) and is writable.
func CheckDataDir(dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	f, err := os.CreateTemp(dir, ".write-check-*")
	if err != nil {
		return fmt.Errorf("data dir %s is not writable: %w", dir, err)
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		return fmt.Errorf("close write check file: %w", err)
	}
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("remove write check file: %w", err)
	}
	return nil
}

// lookup resolves a setting from env, env _FILE, then the YAML file.
type lookup struct {
	getenv func(string) string
	file   map[string]string
}

func (l lookup) must(name string) string {
	v, _ := l.get(name) // only secrets can fail (unreadable _FILE), and they use get directly
	return v
}

func (l lookup) get(name string) (string, error) {
	if v := l.getenv(envPrefix + name); v != "" {
		return v, nil
	}
	if secretNames[name] {
		if path := l.getenv(envPrefix + name + "_FILE"); path != "" {
			b, err := os.ReadFile(filepath.Clean(path))
			if err != nil {
				return "", fmt.Errorf("read %s%s_FILE: %w", envPrefix, name, err)
			}
			return strings.TrimSpace(string(b)), nil
		}
	}
	return l.file[strings.ToLower(name)], nil
}

func parseYAML(b []byte) (map[string]string, error) {
	var raw map[string]any
	dec := yaml.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&raw); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if list, ok := v.([]any); ok {
			parts := make([]string, len(list))
			for i, e := range list {
				parts[i] = fmt.Sprint(e)
			}
			out[strings.ToLower(k)] = strings.Join(parts, ",")
			continue
		}
		out[strings.ToLower(k)] = fmt.Sprint(v)
	}
	return out, nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseBool(s string) (bool, error) {
	switch strings.ToLower(s) {
	case "", "0", "false", "no", "off":
		return false, nil
	case "1", "true", "yes", "on":
		return true, nil
	}
	return false, fmt.Errorf("%q is not a boolean", s)
}

// withRole returns dbURL with its user replaced by role, keeping the password.
// An empty or unparsable URL yields "".
func withRole(dbURL, role string) string {
	if dbURL == "" {
		return ""
	}
	u, err := url.Parse(dbURL)
	if err != nil {
		return ""
	}
	if pw, ok := u.User.Password(); ok {
		u.User = url.UserPassword(role, pw)
	} else {
		u.User = url.User(role)
	}
	return u.String()
}
