package evaltest_test

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/openbekci/openbekci/server/internal/core/access"
	"github.com/openbekci/openbekci/server/internal/core/access/evaltest"
	"github.com/openbekci/openbekci/server/internal/core/access/protocol"
)

// fixtureCase is one entry of testdata/access-protocol/evaluation/cases.json.
type fixtureCase struct {
	Name         string                       `json:"name"`
	TZ           string                       `json:"tz"`
	Now          time.Time                    `json:"now"`
	Point        string                       `json:"point"`
	Uses         map[string]int               `json:"uses"`
	Presentation struct{ Kind, Value string } `json:"presentation"`
	AllowList    []map[string]any             `json:"allow_list"`
	Expect       struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
		EntryID  string `json:"entry_id"`
	} `json:"expect"`
}

// The fixture ids are short names (C1, P1); the evaluator is id-agnostic, so
// the entries are decoded without the UUID validation of the wire format.
type looseEntry struct {
	ID        string              `json:"id"`
	Kind      string              `json:"kind"`
	Value     string              `json:"value"`
	Points    []string            `json:"points"`
	ValidFrom protocol.Time       `json:"valid_from"`
	ValidTo   *protocol.Time      `json:"valid_to"`
	Recurring *protocol.Recurring `json:"recurring"`
	MaxUses   *int                `json:"max_uses"`
}

func TestEvaluationFixtures(t *testing.T) {
	raw, err := os.ReadFile("../../../../testdata/access-protocol/evaluation/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []fixtureCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 7 {
		t.Fatalf("only %d cases", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			loc, err := time.LoadLocation(c.TZ)
			if err != nil {
				t.Fatal(err)
			}
			value := c.Presentation.Value
			switch c.Presentation.Kind {
			case "plate":
				value = access.NormalizePlate(value)
			case "visitor_qr":
				token, ok := cutPrefix(value, access.QRPrefix)
				if !ok {
					t.Fatal("not an obk1 token")
				}
				value = access.QRValue(token)
			}
			var candidates []protocol.Entry
			for _, m := range c.AllowList {
				b, _ := json.Marshal(m)
				var le looseEntry
				if err := json.Unmarshal(b, &le); err != nil {
					t.Fatal(err)
				}
				if le.Kind == c.Presentation.Kind && le.Value == value {
					candidates = append(candidates, protocol.Entry{ID: le.ID, Kind: le.Kind, Value: le.Value, Points: le.Points,
						ValidFrom: le.ValidFrom, ValidTo: le.ValidTo, Recurring: le.Recurring, MaxUses: le.MaxUses})
				}
			}
			out := evaltest.Evaluate(candidates, c.Point, c.Now, loc, func(id string) int { return c.Uses[id] })
			decision := "deny"
			if out.Allowed {
				decision = "allow"
			}
			if decision != c.Expect.Decision || out.Reason != c.Expect.Reason || out.EntryID != c.Expect.EntryID {
				t.Fatalf("got %s/%s/%s, want %s/%s/%s", decision, out.Reason, out.EntryID, c.Expect.Decision, c.Expect.Reason, c.Expect.EntryID)
			}
		})
	}
}

func cutPrefix(s, prefix string) (string, bool) {
	if len(s) > len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):], true
	}
	return "", false
}

// The overnight window is inclusive at both ends of the minute, and the
// morning after belongs to the day the window started on.
func TestOvernightWindowBoundaries(t *testing.T) {
	loc, _ := time.LoadLocation("UTC")
	friNight := protocol.Recurring{Days: []int{5}, Start: "22:00", End: "06:00"}
	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	cases := []struct {
		when string
		want bool
	}{
		{"2026-11-13T21:59:59Z", false}, // Fri 21:59 before the start
		{"2026-11-13T22:00:00Z", true},  // Fri 22:00 start inclusive
		{"2026-11-14T06:00:59Z", true},  // Sat 06:00:59 end inclusive at the minute
		{"2026-11-14T06:01:00Z", false}, // Sat 06:01
		{"2026-11-15T02:00:00Z", false}, // Sun 02:00: Saturday is not in days
		{"2026-11-12T23:00:00Z", false}, // Thu 23:00: Thursday is not in days
	}
	for _, c := range cases {
		if got := evaltest.InRecurring(friNight, at(c.when), loc); got != c.want {
			t.Errorf("%s: got %v, want %v", c.when, got, c.want)
		}
	}
}

func TestTheMostSpecificDenialWins(t *testing.T) {
	loc, _ := time.LoadLocation("UTC")
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	from := protocol.At(now.Add(-time.Hour))
	to := protocol.At(now.Add(-time.Minute))
	wrongPoint := protocol.Entry{ID: "a", Points: []string{"P2"}, ValidFrom: from}
	expired := protocol.Entry{ID: "b", Points: []string{"P1"}, ValidFrom: from, ValidTo: &to}
	out := evaltest.Evaluate([]protocol.Entry{wrongPoint, expired}, "P1", now, loc, nil)
	if out.Allowed || out.Reason != evaltest.ReasonOutsideWindow || out.EntryID != "b" {
		t.Fatalf("got %+v", out)
	}
}
