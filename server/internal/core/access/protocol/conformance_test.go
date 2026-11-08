package protocol_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/openbekci/openbekci/server/internal/core/access/protocol"
)

const (
	fixtureDir = "../../../../testdata/access-protocol/v1"
	schemaDir  = "../../../../../api/agent-protocol/v1"
)

func fixtures(t *testing.T, prefix string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(fixtureDir, prefix+"-*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no %s fixtures in %s: %v", prefix, fixtureDir, err)
	}
	return paths
}

// decodeFixture is what the server does with a frame: strict envelope, then
// the typed payload with its validation.
func decodeFixture(data []byte) error {
	env, err := protocol.ParseEnvelope(data)
	if err != nil {
		return err
	}
	_, err = env.Decode()
	return err
}

func TestValidFixturesAreAccepted(t *testing.T) {
	for _, p := range fixtures(t, "valid") {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := decodeFixture(data); err != nil {
			t.Errorf("%s: rejected: %v", filepath.Base(p), err)
		}
	}
}

func TestInvalidFixturesAreRejected(t *testing.T) {
	for _, p := range fixtures(t, "invalid") {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := decodeFixture(data); err == nil {
			t.Errorf("%s: accepted, want rejected", filepath.Base(p))
		}
	}
}

// compileSchemas loads the served schemas of api/agent-protocol/v1.
func compileSchemas(t *testing.T) map[string]*jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	paths, err := filepath.Glob(filepath.Join(schemaDir, "*.schema.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no schemas in %s", schemaDir)
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		var head struct {
			ID string `json:"$id"`
		}
		_ = json.Unmarshal(data, &head)
		if err := c.AddResource(head.ID, doc); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
	}
	out := map[string]*jsonschema.Schema{}
	for _, name := range []string{"envelope", "hello-agent", "hello-server", "snapshot", "delta", "event", "ack", "command", "heartbeat"} {
		s, err := c.Compile("https://openbekci.org/agent-protocol/v1/" + name + ".schema.json")
		if err != nil {
			t.Fatalf("compile %s: %v", name, err)
		}
		out[name] = s
	}
	return out
}

// schemaFor names the payload schema of an envelope.
func schemaFor(env protocol.Envelope) string {
	if env.Type == protocol.TypeHello {
		var probe struct {
			From string `json:"from"`
		}
		_ = json.Unmarshal(env.Payload, &probe)
		return "hello-" + probe.From
	}
	return env.Type
}

// validAgainstSchemas runs envelope and payload through the served JSON schemas.
func validAgainstSchemas(schemas map[string]*jsonschema.Schema, data []byte) error {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return err
	}
	if err := schemas["envelope"].Validate(doc); err != nil {
		return err
	}
	var env protocol.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return err
	}
	name := schemaFor(env)
	s, ok := schemas[name]
	if !ok {
		return errors.New("no schema for " + name)
	}
	payload, err := jsonschema.UnmarshalJSON(bytes.NewReader(env.Payload))
	if err != nil {
		return err
	}
	return s.Validate(payload)
}

// The served schemas and the typed decoder must agree on every shared fixture.
func TestSchemasAgreeWithTheFixtures(t *testing.T) {
	schemas := compileSchemas(t)
	for _, p := range fixtures(t, "valid") {
		data, _ := os.ReadFile(p)
		if err := validAgainstSchemas(schemas, data); err != nil {
			t.Errorf("%s: the served schema rejects a valid fixture: %v", filepath.Base(p), err)
		}
	}
	for _, p := range fixtures(t, "invalid") {
		data, _ := os.ReadFile(p)
		name := filepath.Base(p)
		err := validAgainstSchemas(schemas, data)
		// Rules a JSON schema cannot state (cross-field: to_version > from_version, part <= parts,
		// a remove without an entry; the UUID version; the grace range) are decoder-only.
		if err == nil && !decoderOnly(name) {
			t.Errorf("%s: the served schema accepts an invalid fixture", name)
		}
	}
}

// decoderOnly lists the invalid fixtures that only the typed decoder can reject.
func decoderOnly(name string) bool {
	for _, k := range []string{"grace", "uuid-v4", "delta-order", "remove-with-entry", "snapshot-part"} {
		if strings.Contains(name, k) {
			return true
		}
	}
	return false
}

// What the server emits must itself be valid: a hello, a command, an event ack.
func TestEmittedMessagesValidate(t *testing.T) {
	schemas := compileSchemas(t)
	hello := protocol.ServerHello{From: "server", ProtocolVersion: 1, ServerTime: protocol.At(timeNow()), HeartbeatSeconds: 30,
		ConfigVersion: 3, Site: protocol.Site{ID: protocol.NewID(), Timezone: "Europe/Istanbul"}, SnapshotVersion: 9}
	ack := protocol.NewAck(protocol.NewID(), protocol.AckOK, protocol.EventAckDetail{
		Accepted: []string{protocol.NewID()}, Duplicates: []string{}, Rejected: []protocol.Rejection{{ID: protocol.NewID(), Code: protocol.RejectInvalid}}})
	for typ, payload := range map[string]any{protocol.TypeHello: hello, protocol.TypeAck: ack} {
		env, err := protocol.New(typ, payload, timeNow())
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(env)
		if err := validAgainstSchemas(schemas, data); err != nil {
			t.Errorf("emitted %s does not validate: %v", typ, err)
		}
		if err := decodeFixture(data); err != nil {
			t.Errorf("emitted %s does not decode: %v", typ, err)
		}
	}
}
