package protocol_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/openbekci/openbekci/server/internal/core/access/protocol"
)

func timeNow() time.Time { return time.Date(2026, 11, 9, 10, 0, 0, 123_000_000, time.UTC) }

func TestEnvelopeRoundTripKeepsMilliseconds(t *testing.T) {
	env, err := protocol.New(protocol.TypeAck, protocol.NewAck(protocol.NewID(), protocol.AckOK, nil), timeNow())
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	_ = json.Unmarshal(data, &wire)
	if wire["ts"] != "2026-11-09T10:00:00.123Z" {
		t.Fatalf("ts = %v", wire["ts"])
	}
	if _, err := protocol.ParseEnvelope(data); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownTypeIsReportedNotRejected(t *testing.T) {
	env := protocol.Envelope{V: 1, Type: "teleport", ID: protocol.NewID(), TS: protocol.At(timeNow()), Payload: json.RawMessage(`{}`)}
	if _, err := env.Decode(); err != protocol.ErrUnknownType {
		t.Fatalf("err = %v, want ErrUnknownType", err)
	}
}

func TestEventValidation(t *testing.T) {
	good := protocol.Event{ID: protocol.NewID(), PointID: protocol.NewID(), Decision: "deny", Reason: "unknown", OccurredAt: protocol.At(timeNow())}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := good
	bad.ID = "0192f1a0-0000-4000-8000-0000000000e1" // v4
	if bad.Validate() == nil {
		t.Fatal("a UUIDv4 event id must be invalid")
	}
	bad = good
	bad.Detail = json.RawMessage(`"not an object"`)
	if bad.Validate() == nil {
		t.Fatal("non-object detail must be invalid")
	}
}

func TestOpenPointLifetimeIsCapped(t *testing.T) {
	issued := protocol.At(timeNow())
	long := protocol.At(timeNow().Add(61 * time.Second))
	c := protocol.Command{Command: protocol.CmdOpenPoint, PointID: protocol.NewID(), IssuedAt: &issued, ExpiresAt: &long}
	if c.Validate() == nil {
		t.Fatal("a 61 s open_point must be invalid")
	}
	ok := protocol.At(timeNow().Add(15 * time.Second))
	c.ExpiresAt = &ok
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}
