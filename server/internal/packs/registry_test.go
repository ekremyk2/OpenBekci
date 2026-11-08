package packs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/openbekci/openbekci/sdk"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// fakePack is a pack whose Register is a function, for the registry rules.
type fakePack struct {
	man      sdk.Manifest
	register func(r sdk.Registry) error
	check    error
}

func (f *fakePack) Manifest() sdk.Manifest { return f.man }
func (f *fakePack) Register(_ context.Context, r sdk.Registry) error {
	if f.register == nil {
		return nil
	}
	return f.register(r)
}
func (f *fakePack) StartupCheck(context.Context) error { return f.check }

type fakeSMS struct{ id string }

func (s fakeSMS) ID() string                                         { return s.id }
func (fakeSMS) Send(context.Context, string, string) (string, error) { return "", nil }

type fakeSchema struct{ org json.RawMessage }

func (s fakeSchema) Schema(k sdk.ScopeKind) json.RawMessage {
	if k == sdk.ScopeOrg {
		return s.org
	}
	return nil
}

type fakeCatalog struct{ keys []string }

func (fakeCatalog) ID() string { return "acme.labels" }
func (c fakeCatalog) Labels(string) map[string]string {
	m := map[string]string{}
	for _, k := range c.keys {
		m[k] = "x"
	}
	return m
}

func manifest(id string) sdk.Manifest {
	return sdk.Manifest{ID: id, Version: "1.0.0", MinSDK: sdk.Version, Countries: []string{"DE"}}
}

func start(t *testing.T, packs ...sdk.Pack) (*Host, error) {
	t.Helper()
	h, err := New(Config{}, packs)
	if err != nil {
		t.Fatal(err)
	}
	return h, h.Start(context.Background())
}

func okHandler(http.ResponseWriter, *http.Request) {}

func TestStartRejectsWhatBreaksTheRules(t *testing.T) {
	v1 := sdk.Version
	tests := []struct {
		name string
		pack *fakePack
		want error
		text string
	}{
		{"bad id", &fakePack{man: manifest("Acme")}, ErrManifest, "lower-case"},
		{"id with underscore", &fakePack{man: manifest("a_b")}, ErrManifest, ""},
		{"reserved id", &fakePack{man: manifest("enablement")}, ErrManifest, "reserved"},
		{"bad version", &fakePack{man: sdk.Manifest{ID: "acme", Version: "one", MinSDK: v1}}, ErrManifest, "semantic"},
		{"bad country", &fakePack{man: sdk.Manifest{ID: "acme", Version: "1.0.0", MinSDK: v1, Countries: []string{"deu"}}}, ErrManifest, "ISO"},
		{"min sdk too new", &fakePack{man: sdk.Manifest{ID: "acme", Version: "1.0.0", MinSDK: "0.1.99"}}, ErrMinSDK, "provides only"},
		{"min sdk other minor", &fakePack{man: sdk.Manifest{ID: "acme", Version: "1.0.0", MinSDK: "0.0.1"}}, ErrMinSDK, "before 1.0"},
		{"register error", &fakePack{man: manifest("acme"), register: func(sdk.Registry) error { return errors.New("no config") }}, ErrRegister, "no config"},
		{"register panic", &fakePack{man: manifest("acme"), register: func(sdk.Registry) error { panic("boom") }}, ErrRegister, "boom"},
		{"startup check", &fakePack{man: manifest("acme"), check: errors.New("missing key")}, ErrStartupCheck, "missing key"},
		{"provider outside the namespace", &fakePack{man: manifest("acme"), register: func(r sdk.Registry) error { r.RegisterSMSProvider(fakeSMS{"other"}); return nil }}, ErrNamespace, "acme"},
		{"provider prefix without dot", &fakePack{man: manifest("acme"), register: func(r sdk.Registry) error { r.RegisterSMSProvider(fakeSMS{"acmeX"}); return nil }}, ErrNamespace, ""},
		{"provider twice", &fakePack{man: manifest("acme"), register: func(r sdk.Registry) error {
			r.RegisterSMSProvider(fakeSMS{"acme"})
			r.RegisterSMSProvider(fakeSMS{"acme"})
			return nil
		}}, ErrCollision, "twice"},
		{"job outside the namespace", &fakePack{man: manifest("acme"), register: func(r sdk.Registry) error {
			r.RegisterJob(sdk.JobDefinition{Kind: "sync", Handler: func(context.Context, sdk.Job) error { return nil }})
			return nil
		}}, ErrNamespace, "job kind"},
		{"job without handler", &fakePack{man: manifest("acme"), register: func(r sdk.Registry) error { r.RegisterJob(sdk.JobDefinition{Kind: "acme.sync"}); return nil }}, ErrInvalid, "handler"},
		{"settings key outside the namespace", &fakePack{man: manifest("acme"), register: func(r sdk.Registry) error {
			r.RegisterSettingsSchema(fakeSchema{json.RawMessage(`{"type":"object","properties":{"api_key":{"type":"string"}}}`)})
			return nil
		}}, ErrNamespace, "acme_"},
		{"settings schema not an object", &fakePack{man: manifest("acme"), register: func(r sdk.Registry) error {
			r.RegisterSettingsSchema(fakeSchema{json.RawMessage(`{"type":"string"}`)})
			return nil
		}}, ErrInvalid, "object"},
		{"settings schema invalid", &fakePack{man: manifest("acme"), register: func(r sdk.Registry) error {
			r.RegisterSettingsSchema(fakeSchema{json.RawMessage(`{"type":"object","properties":{"acme_x":{"type":"nonsense"}}}`)})
			return nil
		}}, ErrInvalid, "settings schema"},
		{"catalog key outside the namespace", &fakePack{man: manifest("acme"), register: func(r sdk.Registry) error {
			r.RegisterCatalog(fakeCatalog{[]string{"status.paid"}})
			return nil
		}}, ErrNamespace, "catalog"},
		{"route that is not public", &fakePack{man: manifest("acme"), register: func(r sdk.Registry) error {
			r.RegisterRoutes(sdk.Route{Method: "POST", Path: "/hook", Handler: okHandler})
			return nil
		}}, ErrInvalid, "sdk.Public"},
		{"route on a reserved path", &fakePack{man: manifest("acme"), register: func(r sdk.Registry) error {
			r.RegisterRoutes(sdk.Public("POST", "/settings", okHandler))
			return nil
		}}, ErrNamespace, "reserved"},
		{"route with a parameter", &fakePack{man: manifest("acme"), register: func(r sdk.Registry) error {
			r.RegisterRoutes(sdk.Public("POST", "/x/{id}", okHandler))
			return nil
		}}, ErrInvalid, "literal"},
		{"route twice", &fakePack{man: manifest("acme"), register: func(r sdk.Registry) error {
			r.RegisterRoutes(sdk.Public("POST", "/hook", okHandler), sdk.Public("POST", "/hook", okHandler))
			return nil
		}}, ErrCollision, "twice"},
		{"subscription to an unknown event", &fakePack{man: manifest("acme"), register: func(r sdk.Registry) error {
			r.Subscribe("ticket.created", "acme.x", func(context.Context, sdk.Event) error { return nil })
			return nil
		}}, ErrInvalid, "unknown event"},
		{"subscription name outside the namespace", &fakePack{man: manifest("acme"), register: func(r sdk.Registry) error {
			r.Subscribe(sdk.EventSiteCreated, "x", func(context.Context, sdk.Event) error { return nil })
			return nil
		}}, ErrNamespace, "subscription"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := start(t, tc.pack)
			if err == nil {
				t.Fatal("startup succeeded, want a named error")
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("error %q does not match %v", err, tc.want)
			}
			if !strings.Contains(err.Error(), tc.text) {
				t.Errorf("error %q lacks %q", err, tc.text)
			}
			var re *RegistryError
			if !errors.As(err, &re) || re.Pack == "" {
				t.Errorf("the error does not name the pack: %v", err)
			}
		})
	}
}

func TestStartRejectsTwoPacksWithOneID(t *testing.T) {
	_, err := start(t, &fakePack{man: manifest("acme")}, &fakePack{man: manifest("acme")})
	if !errors.Is(err, ErrCollision) {
		t.Fatalf("two packs with one id: %v", err)
	}
}

func TestStartReportsEveryProblem(t *testing.T) {
	bad := &fakePack{man: manifest("acme"), register: func(r sdk.Registry) error {
		r.RegisterSMSProvider(fakeSMS{"other"})
		r.RegisterJob(sdk.JobDefinition{Kind: "x", Handler: func(context.Context, sdk.Job) error { return nil }})
		return nil
	}}
	_, err := start(t, bad)
	if !errors.Is(err, ErrNamespace) || strings.Count(err.Error(), "namespace violation") != 2 {
		t.Fatalf("both violations should be reported: %v", err)
	}
}

func TestRegistryFreezesAfterStartup(t *testing.T) {
	var kept sdk.Registry
	good := &fakePack{man: manifest("acme"), register: func(r sdk.Registry) error {
		kept = r
		r.RegisterSMSProvider(fakeSMS{"acme"})
		r.RegisterRoutes(sdk.Public("POST", "/hook", okHandler))
		return nil
	}}
	h, err := start(t, good)
	if err != nil {
		t.Fatal(err)
	}
	pk, ok := h.Lookup("acme")
	if !ok || len(pk.C.SMSProviders) != 1 || len(pk.C.Routes) != 1 {
		t.Fatalf("the contributions were not kept: %+v", pk)
	}
	defer func() {
		r := recover()
		re, ok := r.(*RegistryError)
		if !ok || !errors.Is(re, ErrFrozen) {
			t.Fatalf("a registration after startup panicked with %v, want the frozen registry error", r)
		}
	}()
	kept.RegisterSMSProvider(fakeSMS{"acme.late"})
	t.Fatal("a frozen registry accepted a registration")
}

func TestPanicsInPackCodeAreRecoveredAsPackError(t *testing.T) {
	h, err := start(t, &fakePack{man: manifest("acme")})
	if err != nil {
		t.Fatal(err)
	}
	org := [16]byte{1}
	err = h.call(context.Background(), org, "acme", "handler", func(context.Context) error { panic("kaboom") })
	var pe *problem.Error
	if !errors.As(err, &pe) || pe.Code != problem.CodePackError || pe.Status != 500 {
		t.Fatalf("a panic became %v, want a pack_error problem", err)
	}
	err = h.call(context.Background(), org, "acme", "handler", func(context.Context) error { return errors.New("plain") })
	if !errors.As(err, &pe) || pe.Code != problem.CodePackError {
		t.Fatalf("a plain error became %v, want pack_error", err)
	}
	err = h.call(context.Background(), org, "acme", "handler", func(context.Context) error { return sdk.ErrNotFound })
	if !errors.As(err, &pe) || pe.Status != 404 {
		t.Fatalf("ErrNotFound became %v, want 404", err)
	}
}

func TestMigrationConventions(t *testing.T) {
	before := map[string]tableShape{"person": {cols: 5}, "pack_acme_old": {cols: 2, hasOrg: true, rls: true}}
	good := map[string]tableShape{"person": {cols: 5}, "pack_acme_old": {cols: 3, hasOrg: true, rls: true}, "pack_acme_new": {cols: 4, hasOrg: true, rls: true}, "pack_acme_goose_version": {cols: 4}}
	if err := checkConventions("acme", before, good); err != nil {
		t.Fatalf("a conforming migration was refused: %v", err)
	}
	for name, after := range map[string]map[string]tableShape{
		"table outside the namespace": {"person": {cols: 5}, "pack_acme_old": {cols: 2, hasOrg: true, rls: true}, "acme_stuff": {cols: 1}},
		"a core table altered":        {"person": {cols: 6}, "pack_acme_old": {cols: 2, hasOrg: true, rls: true}},
		"a core table dropped":        {"pack_acme_old": {cols: 2, hasOrg: true, rls: true}},
		"tenant table without rls":    {"person": {cols: 5}, "pack_acme_old": {cols: 2, hasOrg: true, rls: true}, "pack_acme_new": {cols: 2, hasOrg: true}},
		"another pack's namespace":    {"person": {cols: 5}, "pack_acme_old": {cols: 2, hasOrg: true, rls: true}, "pack_acmex_t": {cols: 1}},
	} {
		if err := checkConventions("acme", before, after); !errors.Is(err, ErrMigrations) {
			t.Errorf("%s: %v, want ErrMigrations", name, err)
		}
	}
}
