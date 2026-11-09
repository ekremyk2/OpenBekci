package app

import (
	"strings"
	"testing"
)

func TestLoadConfigPushRelays(t *testing.T) {
	e := baseEnv()
	e["OPENBEKCI_PUSH_RELAYS"] = "official=https://push.openbekci.org;srv_1;/run/secrets/relay;org.openbekci.app, white=https://push.example.org;srv_2;/run/secrets/white"
	c, err := LoadConfig("", env(e))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.PushRelays) != 2 {
		t.Fatalf("relays = %+v", c.PushRelays)
	}
	if r := c.PushRelays[0]; r.ID != "official" || r.URL != "https://push.openbekci.org" || r.ServerID != "srv_1" || r.SecretFile != "/run/secrets/relay" || r.AppID != "org.openbekci.app" {
		t.Errorf("first relay = %+v", r)
	}
	if r := c.PushRelays[1]; r.AppID != "" || r.ID != "white" {
		t.Errorf("second relay = %+v", r)
	}
	if got := pushRelayIDs(c); strings.Join(got, ",") != "official,white" {
		t.Errorf("ids = %v", got)
	}
	if err := c.ValidateServe(); err != nil {
		t.Errorf("ValidateServe: %v", err)
	}
}

func TestLoadConfigPushRelaysRejectsBadEntries(t *testing.T) {
	for _, v := range []string{
		"noequals", "x=ftp://a;s;f", "x=https://a", "x=https://a;s", "has space=https://a;s;f",
		"a=https://a;s;f,a=https://b;s;f", "x=https://a;s;f;app;extra", "x=",
	} {
		e := baseEnv()
		e["OPENBEKCI_PUSH_RELAYS"] = v
		if _, err := LoadConfig("", env(e)); err == nil || !strings.Contains(err.Error(), "PUSH_RELAYS") {
			t.Errorf("%q: err = %v", v, err)
		}
	}
}

func TestValidateServePushRelayLogDriverNeedsInsecureDev(t *testing.T) {
	e := baseEnv()
	e["OPENBEKCI_PUSH_RELAYS"] = "dev=log"
	c, err := LoadConfig("", env(e))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ValidateServe(); err == nil || !strings.Contains(err.Error(), "log driver") {
		t.Errorf("log relay in production: %v", err)
	}
	e["OPENBEKCI_PUSH_RELAYS"] = "x=http://push.example.org;s;f"
	if c, err = LoadConfig("", env(e)); err != nil {
		t.Fatal(err)
	}
	if err := c.ValidateServe(); err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("http relay in production: %v", err)
	}
}
