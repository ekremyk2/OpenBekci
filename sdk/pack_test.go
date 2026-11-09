package sdk

import (
	"context"
	"testing"
)

func TestSupports(t *testing.T) {
	for _, tc := range []struct {
		have, min string
		ok        bool
	}{
		{"0.1.0", "0.1.0", true},
		{"0.1.4", "0.1.2", true},
		{"0.1.0", "0.1.1", false}, // newer than the server
		{"0.2.0", "0.1.0", false}, // before 1.0 another minor may break
		{"1.3.0", "1.1.0", true},
		{"1.0.0", "2.0.0", false},
		{"0.1.0", "x", false},
	} {
		err := supports(tc.have, tc.min)
		if (err == nil) != tc.ok {
			t.Errorf("supports(%q, %q) = %v, want ok=%v", tc.have, tc.min, err, tc.ok)
		}
	}
	if err := Supports(Version); err != nil {
		t.Errorf("the SDK does not support its own version: %v", err)
	}
}

func TestOrgContext(t *testing.T) {
	if _, ok := OrgFrom(context.Background()); ok {
		t.Fatal("empty context has an org")
	}
	if o, ok := OrgFrom(WithOrg(context.Background(), "o1")); !ok || o != "o1" {
		t.Fatalf("OrgFrom = %q, %v", o, ok)
	}
}

func TestRoutesArePublicOnly(t *testing.T) {
	if !Public("POST", "/x", nil).IsPublic() {
		t.Error("Public route is not public")
	}
	if (Route{Method: "GET", Path: "/x"}).IsPublic() {
		t.Error("hand-built route claims to be public")
	}
}

func TestStatementHelpers(t *testing.T) {
	s := Statement{Lines: []StatementLine{{Amount: 100, Raw: map[string]string{"a": "b"}}, {Amount: -30}}}
	if s.Total() != 70 {
		t.Errorf("Total = %d", s.Total())
	}
	if st := s.StripRaw(); st.Lines[0].Raw != nil || s.Lines[0].Raw == nil {
		t.Error("StripRaw must copy and clear Raw")
	}
}
