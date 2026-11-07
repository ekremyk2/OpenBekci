package instance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

type countingIDs struct{ n int }

func (c *countingIDs) InstanceID(context.Context) (uuid.UUID, error) {
	c.n++
	return uuid.MustParse("0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"), nil
}

type mutableMethods struct{ list []AuthMethod }

func (m *mutableMethods) AuthMethods(context.Context) []AuthMethod { return m.list }

func TestDocumentIsCachedForSixtySeconds(t *testing.T) {
	ids := &countingIDs{}
	src := &mutableMethods{list: []AuthMethod{{ID: MethodPassword}}}
	reg := &AuthMethodRegistry{}
	reg.Register(src)
	s := NewService(Config{Name: "X", BaseURL: "https://x.example", ServerVersion: "0.1.0"}, ids, reg)
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	d, err := s.Discovery(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if d.APIBaseURL != "https://x.example/v1" || len(d.AuthMethods) != 1 || d.SDKVersion != nil || d.RelayChallenge != nil {
		t.Fatalf("document = %+v", d)
	}

	src.list = append(src.list, AuthMethod{ID: MethodSMSOTP})
	now = now.Add(59 * time.Second)
	if d, _ = s.Discovery(context.Background()); len(d.AuthMethods) != 1 {
		t.Errorf("within the TTL the cached document must be served, got %d methods", len(d.AuthMethods))
	}
	now = now.Add(2 * time.Second)
	if d, _ = s.Discovery(context.Background()); len(d.AuthMethods) != 2 {
		t.Errorf("after the TTL the document must be rebuilt, got %d methods", len(d.AuthMethods))
	}
	if ids.n != 1 {
		t.Errorf("instance id read %d times, want once (it is stable)", ids.n)
	}
}

func TestInfoHasEmptyPolicies(t *testing.T) {
	s := NewService(Config{}, &countingIDs{}, nil)
	info, err := s.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Policies == nil || len(info.Policies) != 0 || info.AuthMethods == nil {
		t.Errorf("info = %+v", info)
	}
}
