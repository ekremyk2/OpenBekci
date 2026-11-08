package push

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Defaults.
const (
	DefaultTimeout = 10 * time.Second
	DefaultAppID   = "org.openbekci.app"
	// relayBatch is how many messages go into one relay request.
	relayBatch = 100
	maxReply   = 1 << 20
	ttlSeconds = 3600
)

// RelayConfig is one entry of OPENBEKCI_PUSH_RELAYS.
type RelayConfig struct {
	// ID is the relay id devices register with (`official` for the project's relay).
	ID string
	// URL is the relay base URL (https); "log" selects the development driver
	// that logs the payload instead of sending it.
	URL      string
	ServerID string
	// Secret is the HMAC secret of the enrollment (the content of the secret file).
	Secret []byte
	// AppID names the store app the relay holds credentials for.
	AppID string
}

// Options configures a Router.
type Options struct {
	Relays []RelayConfig
	// Timeout bounds one HTTP request; zero means DefaultTimeout.
	Timeout time.Duration
	// AllowPrivate lets the direct UnifiedPush post reach private, loopback and
	// link-local addresses and plain http (a distributor on the LAN, tests).
	// Off by default: the endpoint URL is chosen by the device owner.
	AllowPrivate bool
	// Transport replaces the HTTP transport of relay requests (tests).
	Transport http.RoundTripper
	Log       *slog.Logger
	Now       func() time.Time
}

// Router is the Sender the server uses: relays for fcm and apns, direct
// posts for unifiedpush.
type Router struct {
	relays map[string]RelayConfig
	o      Options
	relay  *http.Client
	direct *http.Client
	log    *slog.Logger
	logger Log
}

// NewRouter builds a Router.
func NewRouter(o Options) *Router {
	if o.Timeout == 0 {
		o.Timeout = DefaultTimeout
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	r := &Router{relays: map[string]RelayConfig{}, o: o, log: o.Log}
	if r.log == nil {
		r.log = slog.Default()
	}
	r.logger = Log{Log: r.log}
	for _, c := range o.Relays {
		if c.AppID == "" {
			c.AppID = DefaultAppID
		}
		r.relays[c.ID] = c
	}
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	rt := http.DefaultTransport
	if o.Transport != nil {
		rt = o.Transport
	}
	r.relay = &http.Client{Timeout: o.Timeout, Transport: rt, CheckRedirect: noRedirect}
	r.direct = &http.Client{Timeout: o.Timeout, CheckRedirect: noRedirect, Transport: directTransport(o.AllowPrivate)}
	return r
}

// RelayIDs lists the configured relay ids (for the discovery document).
func (r *Router) RelayIDs() []string {
	out := make([]string, 0, len(r.relays))
	for _, c := range r.o.Relays {
		out = append(out, c.ID)
	}
	return out
}

// Send implements Sender.
func (r *Router) Send(ctx context.Context, msgs []Message) ([]Result, error) {
	out := make([]Result, len(msgs))
	byRelay := map[string][]int{}
	for i, m := range msgs {
		switch m.Kind {
		case KindUnifiedPush:
			out[i] = r.direct1(ctx, m)
		case KindFCM, KindAPNs:
			byRelay[m.RelayID] = append(byRelay[m.RelayID], i)
		default:
			out[i] = Result{Outcome: InvalidToken, Detail: "unknown push kind"}
		}
	}
	for id, idx := range byRelay {
		cfg, ok := r.relays[id]
		if !ok {
			for _, i := range idx {
				out[i] = Result{Outcome: NoRoute, Detail: ErrNoRoute.Error()}
			}
			continue
		}
		for start := 0; start < len(idx); start += relayBatch {
			end := min(start+relayBatch, len(idx))
			chunk := idx[start:end]
			batch := make([]Message, len(chunk))
			for j, i := range chunk {
				batch[j] = msgs[i]
			}
			res := r.viaRelay(ctx, cfg, batch)
			for j, i := range chunk {
				out[i] = res[j]
			}
		}
	}
	return out, nil
}

type relayMessage struct {
	PushKind   string    `json:"push_kind"`
	Token      string    `json:"token"`
	AppID      string    `json:"app_id"`
	Priority   string    `json:"priority"`
	TTLSeconds int       `json:"ttl_seconds"`
	Data       Payload   `json:"data"`
	APNs       *relayAPN `json:"apns,omitempty"`
}

type relayAPN struct {
	MutableContent  bool   `json:"mutable_content"`
	GenericTitleKey string `json:"generic_title_key"`
}

type relayRequest struct {
	Messages []relayMessage `json:"messages"`
}

type relayReply struct {
	Results []struct {
		Index     *int   `json:"index"`
		Status    string `json:"status"`
		Code      string `json:"code"`
		Retryable bool   `json:"retryable"`
	} `json:"results"`
}

func allError(n int, detail string, o Outcome) []Result {
	out := make([]Result, n)
	for i := range out {
		out[i] = Result{Outcome: o, Detail: detail}
	}
	return out
}

func (r *Router) viaRelay(ctx context.Context, cfg RelayConfig, batch []Message) []Result {
	if cfg.URL == "log" {
		res, _ := r.logger.Send(ctx, batch)
		return res
	}
	req := relayRequest{Messages: make([]relayMessage, len(batch))}
	for i, m := range batch {
		rm := relayMessage{PushKind: m.Kind, Token: m.Token, AppID: cfg.AppID, Priority: "high", TTLSeconds: ttlSeconds, Data: m.Data}
		if m.Kind == KindAPNs {
			rm.APNs = &relayAPN{MutableContent: true, GenericTitleKey: GenericTitleKey(m.Data.Type)}
		}
		req.Messages[i] = rm
	}
	body, err := json.Marshal(req)
	if err != nil {
		return allError(len(batch), "encode relay request", Error)
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return allError(len(batch), "nonce", Error)
	}
	ts := strconv.FormatInt(r.o.Now().Unix(), 10)
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.URL, "/")+"/v1/send", bytes.NewReader(body))
	if err != nil {
		return allError(len(batch), "relay url: "+err.Error(), Error)
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Authorization", RelayAuth(cfg.ServerID, cfg.Secret, ts, hex.EncodeToString(nonce), body))
	resp, err := r.relay.Do(hreq)
	if err != nil {
		return allError(len(batch), "relay unreachable: "+errClass(err), Error)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxReply))
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		// retryable as a whole; the job's backoff spaces the retry (Retry-After is logged for the operator)
		return allError(len(batch), "relay rate limited (retry after "+clip(resp.Header.Get("Retry-After"), 16)+"s)", RateLimited)
	case resp.StatusCode != http.StatusOK:
		// {"error":{"code","message"}}: a clock or nonce problem and any 5xx may
		// pass on a retry; the other 4xx (unauthorized, server_disabled, bad
		// request, too large) will not, so those messages are dropped.
		var e struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		code := clip(e.Error.Code, 64)
		detail := "relay answered " + strconv.Itoa(resp.StatusCode) + " " + code
		if resp.StatusCode >= 500 || code == "stale_timestamp" || code == "replayed_nonce" {
			return allError(len(batch), detail, Error)
		}
		return allError(len(batch), detail, Rejected)
	}
	var rep relayReply
	if err := json.Unmarshal(raw, &rep); err != nil || len(rep.Results) != len(batch) {
		return allError(len(batch), "relay reply is malformed", Error)
	}
	out := make([]Result, len(batch))
	for pos, x := range rep.Results {
		i := pos
		if x.Index != nil && *x.Index >= 0 && *x.Index < len(batch) {
			i = *x.Index
		}
		switch x.Status {
		case "ok":
			out[i] = Result{Outcome: OK}
		case "invalid_token":
			out[i] = Result{Outcome: InvalidToken, Detail: "relay reported the token invalid"}
		case "rate_limited":
			out[i] = Result{Outcome: RateLimited, Detail: "relay rate limited"}
		default:
			o := Rejected
			if x.Retryable {
				o = Error
			}
			out[i] = Result{Outcome: o, Detail: "relay error: " + clip(x.Code, 64)}
		}
	}
	return out
}

// RelayAuth is the value of the Authorization header of a relay request:
// Relay-HMAC <server_id>:<ts>:<nonce>:<hex hmac-sha256(secret, ts "." nonce "." sha256hex(body))>.
func RelayAuth(serverID string, secret []byte, ts, nonce string, body []byte) string {
	sum := sha256.Sum256(body)
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(ts + "." + nonce + "." + hex.EncodeToString(sum[:])))
	return "Relay-HMAC " + serverID + ":" + ts + ":" + nonce + ":" + hex.EncodeToString(m.Sum(nil))
}

// direct1 posts the payload as JSON to a UnifiedPush endpoint.
func (r *Router) direct1(ctx context.Context, m Message) Result {
	u, err := url.Parse(m.Token)
	if err != nil || u.Host == "" || (u.Scheme != "https" && (!r.o.AllowPrivate || u.Scheme != "http")) {
		return Result{Outcome: InvalidToken, Detail: "the endpoint is not an https URL"}
	}
	body, _ := json.Marshal(m.Data)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return Result{Outcome: InvalidToken, Detail: "the endpoint is not a URL"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("TTL", strconv.Itoa(ttlSeconds))
	resp, err := r.direct.Do(req)
	if err != nil {
		if errors.Is(err, errBlockedAddress) {
			return Result{Outcome: InvalidToken, Detail: "the endpoint address is not allowed"}
		}
		return Result{Outcome: Error, Detail: "endpoint unreachable: " + errClass(err)}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxReply))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return Result{Outcome: OK}
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return Result{Outcome: InvalidToken, Detail: "the distributor no longer knows the endpoint"}
	case resp.StatusCode == http.StatusTooManyRequests:
		return Result{Outcome: RateLimited, Detail: "endpoint rate limited"}
	}
	return Result{Outcome: Error, Detail: "endpoint answered " + strconv.Itoa(resp.StatusCode)}
}

var errBlockedAddress = errors.New("push: address not allowed")

// directTransport refuses private, loopback, link-local and unspecified
// addresses at connect time (after DNS resolution) unless allowPrivate.
func directTransport(allowPrivate bool) http.RoundTripper {
	t := http.DefaultTransport.(*http.Transport).Clone()
	if allowPrivate {
		return t
	}
	d := &net.Dialer{Timeout: 5 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return errBlockedAddress
		}
		ip := net.ParseIP(host)
		if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
			return errBlockedAddress
		}
		return nil
	}}
	t.DialContext = d.DialContext
	t.Proxy = nil
	return t
}

// errClass is a short, secret-free description of a transport error.
func errClass(err error) string {
	var ne net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Sprintf("%T", ue.Err)
	}
	return "error"
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
