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
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/openbekci/openbekci/sdk"
	"github.com/openbekci/openbekci/server/internal/platform/db"
	"github.com/openbekci/openbekci/server/internal/platform/sms"
)

const envPrefix = "OPENBEKCI_"

// Config is the process configuration (backend plan, section 17).
type Config struct {
	DatabaseURL         string
	MigratorDatabaseURL string
	PlatformDatabaseURL string
	// DatabaseSidePoolMax caps the second connection pool for transactions
	// opened while another is held (OPENBEKCI_DATABASE_SIDE_POOL_MAX); default 4.
	DatabaseSidePoolMax int

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

	// MaxUploadBytes limits one uploaded file (OPENBEKCI_MAX_UPLOAD_BYTES);
	// zero means 25 MiB. It bypasses the JSON cap above for the multipart
	// upload route only.
	MaxUploadBytes int64
	// FilesAllowedExtensions narrows the built-in file type allowlist
	// (OPENBEKCI_FILES_ALLOWED_EXTENSIONS, comma separated); empty keeps it all.
	FilesAllowedExtensions []string
	// StorageDriver is "local" (default, under DATA_DIR/files) or "s3".
	StorageDriver string
	// S3 driver settings (OPENBEKCI_STORAGE_S3_*).
	StorageS3Bucket    string
	StorageS3Region    string
	StorageS3Endpoint  string
	StorageS3AccessKey string
	StorageS3SecretKey string
	StorageS3PathStyle bool
	StorageS3Prefix    string
	// ClamAVAddr is a clamd address (host:port or unix:/path); empty skips
	// virus scanning (OPENBEKCI_CLAMAV_ADDR).
	ClamAVAddr string
	// ClamAVTimeout bounds one virus scan (OPENBEKCI_CLAMAV_TIMEOUT, default 60s).
	ClamAVTimeout time.Duration
	// MaxImagePixels refuses uploaded images with more pixels than this
	// (OPENBEKCI_MAX_IMAGE_PIXELS, default 25 million).
	MaxImagePixels int
	// ImageWorkers is how many images the process decodes at once
	// (OPENBEKCI_IMAGE_WORKERS, default max(1, NumCPU/2)).
	ImageWorkers int
	// Access control: the largest allow-list of one site that is sent as a snapshot
	// (OPENBEKCI_ACCESS_MAX_SNAPSHOT_ENTRIES, default 20000), and the size of one
	// snapshot part (OPENBEKCI_ACCESS_SNAPSHOT_PART_ENTRIES, at most 2000, the protocol
	// limit; OPENBEKCI_ACCESS_SNAPSHOT_PART_BYTES, default 900000). Zero means the default.
	AccessMaxSnapshotEntries, AccessSnapshotPartEntries, AccessSnapshotPartBytes int

	// Worker runs the job worker inside serve.
	Worker bool
	// MetricsToken enables GET /metrics (OPENBEKCI_METRICS_TOKEN, bearer); MetricsAddr
	// (OPENBEKCI_METRICS_ADDR) makes a worker-only process listen for /metrics and /healthz.
	MetricsToken string
	MetricsAddr  string
	AutoMigrate  bool
	InsecureDev  bool
	Debug        bool

	// ValidateResponses checks every API response against the spec (a dev aid).
	ValidateResponses bool

	// Instance identity and branding for the discovery document.
	InstanceName         string
	MinAppVersionAndroid string
	MinAppVersionIOS     string
	BrandingLogoURL      string
	BrandingPrimaryColor string

	// SMSProvider selects the SMS gateway: "netgsm", "log" (development) or ""
	// (SMS disabled: sign-in by SMS reports sms_unavailable).
	SMSProvider        string
	SMSNetgsmUsercode  string
	SMSNetgsmPassword  string
	SMSNetgsmMsgHeader string

	// MailProvider selects outgoing email: "smtp", "log" (development) or "".
	// Empty means smtp when SMTP_HOST is set and disabled otherwise.
	MailProvider string
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string
	// SMTPTLS is starttls (default), tls or none.
	SMTPTLS string

	// Send budgets of the sign-in messages (OPENBEKCI_SEND_DAILY_BUDGET,
	// SMS_HOURLY_BUDGET, MAIL_HOURLY_BUDGET): per identifier per day, and per
	// instance per hour for SMS and for mail. Zero takes the default (10, 120, 300);
	// a negative value removes the limit.
	SendDailyBudget  int
	SMSHourlyBudget  int
	MailHourlyBudget int

	// PushRelays are the relays devices may register with
	// (OPENBEKCI_PUSH_RELAYS, comma separated `relay_id=url;server_id;secret_file;app_id`);
	// the id `official` is just a name. `relay_id=log` is the development
	// driver that logs the payload. Empty: no push through a relay.
	PushRelays []PushRelayEntry
	// PushAllowPrivate lets UnifiedPush endpoints be plain http or on private
	// addresses (OPENBEKCI_PUSH_ALLOW_PRIVATE, default false).
	PushAllowPrivate bool
	// Packs are the packs compiled into the binary (cmd/openbekci/packs.go); set by the command.
	Packs []sdk.Pack
	// SMSPicker routes an organization's messages to the SMS provider it chose from its enabled
	// packs; set by the server wiring, not by the environment.
	SMSPicker sms.Picker
	// APIKeyRatePerMinute limits requests per organization API key
	// (OPENBEKCI_API_KEY_RATE_PER_MINUTE); zero takes the default (600), a
	// negative value removes the limit.
	APIKeyRatePerMinute int
	// WebhookMaxAttempts bounds delivery attempts per (event, endpoint)
	// (OPENBEKCI_WEBHOOK_MAX_ATTEMPTS); zero takes the default (8).
	WebhookMaxAttempts int
	// WebhookAllowedCIDRs are private, loopback or link-local ranges webhooks
	// may reach (OPENBEKCI_WEBHOOK_ALLOWED_CIDRS, comma separated CIDRs or
	// bare IPs). Empty keeps the SSRF guard strict.
	WebhookAllowedCIDRs []netip.Prefix

	// ServerVersion is the build version; set by the command, not by the environment.
	ServerVersion string
}

// PushRelayEntry is one parsed OPENBEKCI_PUSH_RELAYS entry. The secret file is
// read when the server starts.
type PushRelayEntry struct {
	ID, URL, ServerID, SecretFile, AppID string
}

var pushRelayIDRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// ParsePushRelays parses the OPENBEKCI_PUSH_RELAYS list.
func ParsePushRelays(items []string) ([]PushRelayEntry, error) {
	var out []PushRelayEntry
	seen := map[string]bool{}
	for _, item := range items {
		id, rest, ok := strings.Cut(item, "=")
		id = strings.TrimSpace(id)
		if !ok || !pushRelayIDRe.MatchString(id) {
			return nil, fmt.Errorf("%sPUSH_RELAYS: %q is not relay_id=url;server_id;secret_file;app_id", envPrefix, item)
		}
		if seen[id] {
			return nil, fmt.Errorf("%sPUSH_RELAYS: relay id %q is listed twice", envPrefix, id)
		}
		seen[id] = true
		parts := strings.Split(rest, ";")
		e := PushRelayEntry{ID: id, URL: strings.TrimSpace(parts[0])}
		if e.URL == "log" {
			out = append(out, e)
			continue
		}
		u, err := url.Parse(e.URL)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
			return nil, fmt.Errorf("%sPUSH_RELAYS: relay %q needs an http(s) URL (or log)", envPrefix, id)
		}
		if len(parts) < 3 || strings.TrimSpace(parts[1]) == "" || strings.TrimSpace(parts[2]) == "" || len(parts) > 4 {
			return nil, fmt.Errorf("%sPUSH_RELAYS: relay %q needs url;server_id;secret_file[;app_id]", envPrefix, id)
		}
		e.ServerID, e.SecretFile = strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2])
		if len(parts) == 4 {
			e.AppID = strings.TrimSpace(parts[3])
		}
		out = append(out, e)
	}
	return out, nil
}

// secretNames are the variables that accept a _FILE variant.
var secretNames = map[string]bool{
	"DATABASE_URL": true, "MIGRATOR_DATABASE_URL": true, "PLATFORM_DATABASE_URL": true,
	"SECRET_KEY": true, "ENCRYPTION_KEY": true, "SMS_NETGSM_PASSWORD": true, "SMTP_PASSWORD": true,
	"STORAGE_S3_SECRET_KEY": true, "METRICS_TOKEN": true,
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

	if err = c.loadMessaging(l); err != nil {
		return Config{}, err
	}

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

	c.DatabaseSidePoolMax = db.DefaultSidePoolMax
	if v := l.must("DATABASE_SIDE_POOL_MAX"); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 1 {
			return Config{}, fmt.Errorf("%sDATABASE_SIDE_POOL_MAX: %q is not a positive integer", envPrefix, v)
		}
		c.DatabaseSidePoolMax = n
	}

	if err = c.loadFiles(l); err != nil {
		return Config{}, err
	}
	if err = c.loadIntegrations(l); err != nil {
		return Config{}, err
	}

	for _, o := range []struct {
		name string
		dst  *int
	}{
		{"ACCESS_MAX_SNAPSHOT_ENTRIES", &c.AccessMaxSnapshotEntries},
		{"ACCESS_SNAPSHOT_PART_ENTRIES", &c.AccessSnapshotPartEntries},
		{"ACCESS_SNAPSHOT_PART_BYTES", &c.AccessSnapshotPartBytes},
	} {
		if v := l.must(o.name); v != "" {
			n, perr := strconv.Atoi(v)
			if perr != nil || n < 1 {
				return Config{}, fmt.Errorf("%s%s: %q is not a positive integer", envPrefix, o.name, v)
			}
			*o.dst = n
		}
	}

	if c.MetricsToken, err = l.get("METRICS_TOKEN"); err != nil {
		return Config{}, err
	}
	c.MetricsAddr = l.must("METRICS_ADDR")
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

// loadIntegrations reads the API key and webhook settings.
func (c *Config) loadIntegrations(l lookup) error {
	if v := l.must("API_KEY_RATE_PER_MINUTE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("%sAPI_KEY_RATE_PER_MINUTE: %q is not an integer (negative removes the limit)", envPrefix, v)
		}
		c.APIKeyRatePerMinute = n
	}
	if v := l.must("WEBHOOK_MAX_ATTEMPTS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 20 {
			return fmt.Errorf("%sWEBHOOK_MAX_ATTEMPTS: %q is not an integer from 1 to 20", envPrefix, v)
		}
		c.WebhookMaxAttempts = n
	}
	for _, item := range splitList(l.must("WEBHOOK_ALLOWED_CIDRS")) {
		if p, err := netip.ParsePrefix(item); err == nil {
			c.WebhookAllowedCIDRs = append(c.WebhookAllowedCIDRs, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(item)
		if err != nil {
			return fmt.Errorf("%sWEBHOOK_ALLOWED_CIDRS: %q is not a CIDR or IP address", envPrefix, item)
		}
		c.WebhookAllowedCIDRs = append(c.WebhookAllowedCIDRs, netip.PrefixFrom(a, a.BitLen()))
	}
	return nil
}

// loadFiles reads the file storage and upload settings.
func (c *Config) loadFiles(l lookup) error {
	var err error
	if v := l.must("MAX_UPLOAD_BYTES"); v != "" {
		n, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil || n < 1 {
			return fmt.Errorf("%sMAX_UPLOAD_BYTES: %q is not a positive integer", envPrefix, v)
		}
		c.MaxUploadBytes = n
	}
	c.FilesAllowedExtensions = splitList(l.must("FILES_ALLOWED_EXTENSIONS"))
	c.StorageDriver = strings.ToLower(l.must("STORAGE_DRIVER"))
	switch c.StorageDriver {
	case "", "local", "s3":
	default:
		return fmt.Errorf("%sSTORAGE_DRIVER: %q is not one of local, s3", envPrefix, c.StorageDriver)
	}
	c.StorageS3Bucket = l.must("STORAGE_S3_BUCKET")
	c.StorageS3Region = l.must("STORAGE_S3_REGION")
	c.StorageS3Endpoint = l.must("STORAGE_S3_ENDPOINT")
	c.StorageS3AccessKey = l.must("STORAGE_S3_ACCESS_KEY")
	if c.StorageS3SecretKey, err = l.get("STORAGE_S3_SECRET_KEY"); err != nil {
		return err
	}
	c.StorageS3Prefix = l.must("STORAGE_S3_PREFIX")
	if c.StorageS3PathStyle, err = parseBool(l.must("STORAGE_S3_PATH_STYLE")); err != nil {
		return fmt.Errorf("%sSTORAGE_S3_PATH_STYLE: %w", envPrefix, err)
	}
	if c.StorageDriver == "s3" && c.StorageS3Bucket == "" {
		return fmt.Errorf("%sSTORAGE_DRIVER=s3 needs STORAGE_S3_BUCKET", envPrefix)
	}
	c.ClamAVAddr = l.must("CLAMAV_ADDR")
	if v := l.must("CLAMAV_TIMEOUT"); v != "" {
		d, derr := time.ParseDuration(v)
		if derr != nil || d <= 0 {
			return fmt.Errorf("%sCLAMAV_TIMEOUT: %q is not a positive duration such as 60s", envPrefix, v)
		}
		c.ClamAVTimeout = d
	}
	for _, s := range []struct {
		name string
		dst  *int
	}{{"MAX_IMAGE_PIXELS", &c.MaxImagePixels}, {"IMAGE_WORKERS", &c.ImageWorkers}} {
		if v := l.must(s.name); v != "" {
			n, perr := strconv.Atoi(v)
			if perr != nil || n < 1 {
				return fmt.Errorf("%s%s: %q is not a positive integer", envPrefix, s.name, v)
			}
			*s.dst = n
		}
	}
	return nil
}

// loadMessaging reads the SMS and email settings.
func (c *Config) loadMessaging(l lookup) error {
	var err error
	c.SMSProvider = strings.ToLower(l.must("SMS_PROVIDER"))
	switch c.SMSProvider {
	case "", "log", "netgsm":
	default:
		return fmt.Errorf("%sSMS_PROVIDER: %q is not one of netgsm, log (or empty to disable SMS)", envPrefix, c.SMSProvider)
	}
	c.SMSNetgsmUsercode = l.must("SMS_NETGSM_USERCODE")
	if c.SMSNetgsmPassword, err = l.get("SMS_NETGSM_PASSWORD"); err != nil {
		return err
	}
	c.SMSNetgsmMsgHeader = l.must("SMS_NETGSM_MSGHEADER")
	if c.SMSProvider == "netgsm" && (c.SMSNetgsmUsercode == "" || c.SMSNetgsmPassword == "" || c.SMSNetgsmMsgHeader == "") {
		return fmt.Errorf("%sSMS_PROVIDER=netgsm needs SMS_NETGSM_USERCODE, SMS_NETGSM_PASSWORD and SMS_NETGSM_MSGHEADER", envPrefix)
	}

	c.MailProvider = strings.ToLower(l.must("MAIL_PROVIDER"))
	c.SMTPHost = l.must("SMTP_HOST")
	if c.MailProvider == "" && c.SMTPHost != "" {
		c.MailProvider = "smtp"
	}
	switch c.MailProvider {
	case "", "log", "smtp":
	default:
		return fmt.Errorf("%sMAIL_PROVIDER: %q is not one of smtp, log (or empty to disable email)", envPrefix, c.MailProvider)
	}
	if p := l.must("SMTP_PORT"); p != "" {
		if c.SMTPPort, err = strconv.Atoi(p); err != nil || c.SMTPPort < 1 || c.SMTPPort > 65535 {
			return fmt.Errorf("%sSMTP_PORT: %q is not a port number", envPrefix, p)
		}
	}
	c.SMTPUsername = l.must("SMTP_USERNAME")
	if c.SMTPPassword, err = l.get("SMTP_PASSWORD"); err != nil {
		return err
	}
	c.SMTPFrom = l.must("SMTP_FROM")
	c.SMTPTLS = strings.ToLower(l.must("SMTP_TLS"))
	for name, dst := range map[string]*int{
		"SEND_DAILY_BUDGET": &c.SendDailyBudget, "SMS_HOURLY_BUDGET": &c.SMSHourlyBudget, "MAIL_HOURLY_BUDGET": &c.MailHourlyBudget,
	} {
		if v := l.must(name); v != "" {
			n, perr := strconv.Atoi(v)
			if perr != nil {
				return fmt.Errorf("%s%s: %q is not an integer (negative removes the limit)", envPrefix, name, v)
			}
			*dst = n
		}
	}
	if c.MailProvider == "smtp" && (c.SMTPHost == "" || c.SMTPFrom == "") {
		return fmt.Errorf("%sMAIL_PROVIDER=smtp needs SMTP_HOST and SMTP_FROM", envPrefix)
	}
	if c.PushRelays, err = ParsePushRelays(splitList(l.must("PUSH_RELAYS"))); err != nil {
		return err
	}
	if c.PushAllowPrivate, err = parseBool(l.must("PUSH_ALLOW_PRIVATE")); err != nil {
		return fmt.Errorf("%sPUSH_ALLOW_PRIVATE: %w", envPrefix, err)
	}
	return nil
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
	// The log drivers write sign-in codes and links to the log.
	if (c.SMSProvider == "log" || c.MailProvider == "log") && !c.InsecureDev {
		errs = append(errs, fmt.Errorf("%sSMS_PROVIDER=log and %sMAIL_PROVIDER=log are for development only; set %sINSECURE_DEV=true to use them", envPrefix, envPrefix, envPrefix))
	}
	for _, r := range c.PushRelays {
		if r.URL == "log" && !c.InsecureDev {
			errs = append(errs, fmt.Errorf("%sPUSH_RELAYS: relay %q uses the log driver, which is for development only; set %sINSECURE_DEV=true to use it", envPrefix, r.ID, envPrefix))
		}
		if strings.HasPrefix(r.URL, "http://") && !c.InsecureDev {
			errs = append(errs, fmt.Errorf("%sPUSH_RELAYS: relay %q must be https (http is for development with INSECURE_DEV)", envPrefix, r.ID))
		}
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

// isLocalHost reports whether host (without port or brackets) is the loopback
// name or address a development instance is served on.
func isLocalHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func (c Config) validateBaseURL() error {
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("%sBASE_URL %q is not an http(s) URL", envPrefix, c.BaseURL)
	}
	local := isLocalHost(u.Hostname())
	// INSECURE_DEV relaxes cookies, key checks and the log drivers; it must
	// not be possible to leave it on behind a real address.
	if c.InsecureDev && !local {
		return fmt.Errorf("%sINSECURE_DEV=true is accepted only when %sBASE_URL is localhost, 127.0.0.1 or [::1] (it is %q); unset it for any other host", envPrefix, envPrefix, u.Hostname())
	}
	if u.Scheme == "http" {
		switch {
		case !local:
			return fmt.Errorf("%sBASE_URL must be https (http is allowed only for localhost with INSECURE_DEV)", envPrefix)
		case !c.InsecureDev:
			return fmt.Errorf("%sBASE_URL uses http; set %sINSECURE_DEV=true for local development", envPrefix, envPrefix)
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
