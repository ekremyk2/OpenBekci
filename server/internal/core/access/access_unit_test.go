package access

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/openbekci/openbekci/server/internal/core/access/protocol"
)

func TestCorrectOccurred(t *testing.T) {
	synced := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	ms := func(d time.Duration) *int64 { v := d.Milliseconds(); return &v }
	cases := []struct {
		name        string
		reported    time.Time
		offset      *int64
		want        time.Time
		wantRawKept bool
	}{
		{"no offset", synced.Add(-time.Minute), nil, synced.Add(-time.Minute), false},
		{"an agent clock 30 s slow is corrected", synced.Add(-time.Minute), ms(30 * time.Second), synced.Add(-30 * time.Second), true},
		{"a small offset is not worth recording", synced.Add(-time.Minute), ms(2 * time.Second), synced.Add(-58 * time.Second), false},
		{"never later than the receive time", synced.Add(-10 * time.Second), ms(10 * time.Minute), synced, true},
		{"an agent clock that is ahead is pulled back", synced.Add(-time.Minute), ms(-20 * time.Second), synced.Add(-80 * time.Second), true},
		{"beyond 24 h the offset is ignored", synced.Add(-time.Hour), ms(25 * time.Hour), synced.Add(-time.Hour), false},
		{"exactly 24 h is still applied", synced.Add(-48 * time.Hour), ms(24 * time.Hour), synced.Add(-24 * time.Hour), true},
		{"a reported time in the future is clamped", synced.Add(time.Hour), nil, synced, true},
	}
	for _, c := range cases {
		got, raw := correctOccurred(c.reported, c.offset, synced)
		if !got.Equal(c.want) {
			t.Errorf("%s: occurred_at = %s, want %s", c.name, got, c.want)
		}
		if (raw != nil) != c.wantRawKept {
			t.Errorf("%s: reported_at kept = %v, want %v", c.name, raw != nil, c.wantRawKept)
		}
		if raw != nil && !raw.Equal(c.reported) {
			t.Errorf("%s: kept %s, want the reported %s", c.name, raw, c.reported)
		}
	}
}

func TestClockSuspect(t *testing.T) {
	off := func(d time.Duration) *int64 { v := d.Milliseconds(); return &v }
	if clockSuspect(false, nil) || clockSuspect(false, off(100*time.Second)) {
		t.Error("100 s is within tolerance")
	}
	if !clockSuspect(false, off(121*time.Second)) || !clockSuspect(false, off(-3*time.Minute)) {
		t.Error("more than 120 s either way is suspect")
	}
	if !clockSuspect(true, nil) {
		t.Error("the agent's own report is believed")
	}
}

func TestResolvePointsFollowsTheScope(t *testing.T) {
	gate, door, off := uuid.New(), uuid.New(), uuid.New()
	pts := []pointRow{
		{ID: gate, Kind: PointVehicleGate, Enabled: true},
		{ID: door, Kind: PointPedestrianDoor, Enabled: true},
		{ID: off, Kind: PointVehicleGate, Enabled: false},
	}
	if got := resolvePoints(ScopeAllVehicle, nil, pts); len(got) != 1 || got[0] != gate.String() {
		t.Errorf("all_vehicle: %v (a disabled point is omitted)", got)
	}
	if got := resolvePoints(ScopeAllPedestrian, nil, pts); len(got) != 1 || got[0] != door.String() {
		t.Errorf("all_pedestrian: %v", got)
	}
	if got := resolvePoints(ScopeAll, nil, pts); len(got) != 2 {
		t.Errorf("all: %v", got)
	}
	if got := resolvePoints(ScopeIDs, []uuid.UUID{door, off}, pts); len(got) != 1 || got[0] != door.String() {
		t.Errorf("ids: %v", got)
	}
	if got := resolvePoints(ScopeAll, nil, nil); got != nil {
		t.Errorf("no points: %v", got)
	}
}

func TestDesiredEntry(t *testing.T) {
	gate := pointRow{ID: uuid.New(), Kind: PointVehicleGate, Enabled: true}
	rec := json.RawMessage(`{"days":[1,3],"start":"22:00","end":"06:00"}`)
	to := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	one := 1
	c := credRow{ID: uuid.New(), Kind: KindPlate, Value: "34ABC123", Status: StatusActive, Scope: ScopeAllVehicle,
		ValidFrom: time.Date(2026, 11, 1, 0, 0, 0, 123_456_000, time.UTC), ValidTo: &to, Recurring: rec, MaxUses: &one}
	e, err := desiredEntry(c, []pointRow{gate})
	if err != nil || e == nil {
		t.Fatalf("entry: %v %v", e, err)
	}
	if e.Recurring == nil || e.Recurring.Start != "22:00" || *e.MaxUses != 1 || e.ValidTo == nil || len(e.Points) != 1 {
		t.Fatalf("entry: %+v", e)
	}
	if e.ValidFrom.Nanosecond() != 123_000_000 {
		t.Errorf("valid_from is carried at millisecond precision, got %d ns", e.ValidFrom.Nanosecond())
	}
	for _, status := range []string{StatusSuspended, StatusRevoked, StatusExpired} {
		c.Status = status
		if e, _ := desiredEntry(c, []pointRow{gate}); e != nil {
			t.Errorf("a %s credential has no entry", status)
		}
	}
	c.Status = StatusActive
	if e, _ := desiredEntry(c, nil); e != nil {
		t.Error("without a point to open there is no entry")
	}
	c.Recurring = json.RawMessage(`{"days":[9],"start":"22:00","end":"06:00"}`)
	if _, err := desiredEntry(c, []pointRow{gate}); err == nil {
		t.Error("an invalid recurring window must not be published")
	}
}

func TestCanonIgnoresRepresentation(t *testing.T) {
	a := protocol.Entry{Points: []string{"b", "a"}, ValidFrom: protocol.At(time.Date(2026, 1, 1, 0, 0, 0, 500_000, time.UTC))}
	b := protocol.Entry{Points: []string{"a", "b"}, ValidFrom: protocol.At(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))}
	ca, cb := canon(a), canon(b)
	if ca.Points[0] != cb.Points[0] || !ca.ValidFrom.Equal(cb.ValidFrom.Time) {
		t.Fatalf("%+v != %+v", ca, cb)
	}
}

func TestAgentTokenShape(t *testing.T) {
	agent := uuid.New()
	tok, err := newAgentToken(agent)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := parseAgentToken(tok)
	if !ok || got != agent {
		t.Fatalf("parse(%q) = %v %v", tok, got, ok)
	}
	for _, bad := range []string{"", "obk_agt_", "obk_agt_" + agent.String(), "obk_agt_" + agent.String() + ".short", "obk_enr_xyz", "Bearer " + tok, "obk_agt_not-a-uuid.0123456789012345678901234567"} {
		if _, ok := parseAgentToken(bad); ok {
			t.Errorf("%q must not parse", bad)
		}
	}
	other, _ := newAgentToken(agent)
	if tok == other {
		t.Error("tokens must be random")
	}
	if len(tok) < 80 {
		t.Errorf("a 256-bit secret expected, token is %d long", len(tok))
	}
}

func TestPublicURLsRequireHTTPS(t *testing.T) {
	s := &Service{cfg: Config{BaseURL: "https://bekci.example.org/"}}
	ws, poll, err := s.publicURLs()
	if err != nil || ws != "wss://bekci.example.org/v1/agent/ws" || poll != "https://bekci.example.org/v1/agent/poll" {
		t.Fatalf("%q %q %v", ws, poll, err)
	}
	s = &Service{cfg: Config{BaseURL: "http://bekci.example.org"}}
	if _, _, err := s.publicURLs(); err == nil {
		t.Fatal("an http base URL must not be handed to agents")
	}
	s = &Service{cfg: Config{BaseURL: "http://127.0.0.1:8484", InsecureDev: true}}
	if ws, poll, err := s.publicURLs(); err != nil || ws != "ws://127.0.0.1:8484/v1/agent/ws" || poll != "http://127.0.0.1:8484/v1/agent/poll" {
		t.Fatalf("dev: %q %q %v", ws, poll, err)
	}
}

func TestReadSettingsClampsTheGrace(t *testing.T) {
	d := readSettings([]byte(`{}`))
	if d.OfflineGraceHours != 72 || d.PlateMatch != "exact" || d.MaintenanceHour != 3 || d.AgentSilentMinutes != 5 {
		t.Fatalf("defaults: %+v", d)
	}
	s := readSettings([]byte(`{"access":{"offline_grace_hours":336,"plate_match":"tolerant","maintenance_hour":0,"agent_silent_minutes":10}}`))
	if s.OfflineGraceHours != 336 || s.PlateMatch != "tolerant" || s.MaintenanceHour != 0 || s.AgentSilentMinutes != 10 {
		t.Fatalf("set: %+v", s)
	}
	for _, bad := range []string{`{"access":{"offline_grace_hours":0}}`, `{"access":{"offline_grace_hours":337}}`, `{"access":{"offline_grace_hours":-5}}`} {
		if g := readSettings([]byte(bad)).OfflineGraceHours; g != 72 {
			t.Errorf("%s: grace %d, want the default 72 (the agent accepts 1..336)", bad, g)
		}
	}
	if readSettings([]byte(`not json`)).OfflineGraceHours != 72 {
		t.Error("garbage keeps the defaults")
	}
}

func TestConfigHashFollowsContent(t *testing.T) {
	agent, site := uuid.New(), uuid.New()
	gate := pointRow{ID: uuid.New(), Name: "Gate", Kind: PointVehicleGate, Direction: "in", AgentID: &agent, DriverRef: &protocol.DriverRef{Instance: "relay", Channel: "1"}, Enabled: true}
	c := siteConfig{Timezone: "Europe/Istanbul", Settings: readSettings(nil), Points: []pointRow{gate}}
	h1 := c.hash()
	if c.hash() != h1 {
		t.Fatal("the hash must be deterministic")
	}
	c.Timezone = "UTC"
	if c.hash() == h1 {
		t.Fatal("a timezone change must change the hash")
	}
	c.Timezone = "Europe/Istanbul"
	c.Points[0].Enabled = false
	if c.hash() == h1 {
		t.Fatal("disabling a point must change the hash")
	}
	// update_config carries only this agent's points that have a driver reference
	c.Points = []pointRow{gate, {ID: uuid.New(), Name: "Foreign", AgentID: ptr(uuid.New()), DriverRef: &protocol.DriverRef{Instance: "x"}, Enabled: true},
		{ID: uuid.New(), Name: "No driver", AgentID: &agent, Enabled: true}}
	cmd := c.updateConfigFor(7, site, agent)
	if err := cmd.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(cmd.Points) != 1 || cmd.Points[0].Name != "Gate" || *cmd.ConfigVersion != 7 || cmd.Settings.HeartbeatSeconds != HeartbeatSeconds {
		t.Fatalf("%+v", cmd)
	}
}

func ptr[T any](v T) *T { return &v }

func TestEventPayloadKeepsTheReportedTime(t *testing.T) {
	reported := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	raw, err := eventPayload(json.RawMessage(`{"confidence":0.9}`), &reported)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if m["confidence"] != 0.9 || m["reported_at"] != "2026-11-09T10:00:00Z" {
		t.Fatalf("%s", raw)
	}
	if raw, _ := eventPayload(nil, nil); raw != nil {
		t.Fatalf("nothing to store, got %s", raw)
	}
	if _, err := eventPayload(json.RawMessage(`[1]`), nil); err == nil {
		t.Fatal("a detail that is not an object is invalid")
	}
}
