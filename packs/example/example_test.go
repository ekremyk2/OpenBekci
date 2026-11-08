// SPDX-License-Identifier: AGPL-3.0-or-later

package example

import (
	"context"
	"testing"
	"time"

	"github.com/openbekci/openbekci/sdk"
)

// These tests need nothing but the SDK. The tests that boot the core (the
// conformance suites, settings, presets, routes, jobs) live in
// server/internal/sdktest, because the harness imports the core.

func TestManifestIsValid(t *testing.T) {
	m := New().Manifest()
	if m.ID != ID || m.Version == "" || len(m.Countries) == 0 {
		t.Fatalf("manifest = %+v", m)
	}
	if err := sdk.Supports(m.MinSDK); err != nil {
		t.Fatalf("the pack does not build against this SDK: %v", err)
	}
}

func TestImporterParse(t *testing.T) {
	data := "example-bank-v1\ndate;amount;counterparty;description;reference;balance\n" +
		"03.02.2026;1.250,00;Ayse;Dues;A-1;10.250,00\n" +
		"05.02.2026;-30,5;Bank;Fee;;10.219,50\n" +
		"xx;1,00;;;;\n"
	st, err := importer{}.Parse(context.Background(), "x.csv", []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Lines) != 2 || len(st.Errors) != 1 || st.Errors[0].Field != "date" {
		t.Fatalf("lines %d, errors %v", len(st.Lines), st.Errors)
	}
	if st.Lines[0].Amount != 125000 || st.Lines[1].Amount != -3050 || st.Lines[0].BookedOn != "2026-02-03" {
		t.Fatalf("lines = %+v", st.Lines)
	}
	if st.OpeningBalance == nil || *st.OpeningBalance != 900000 {
		t.Fatalf("opening balance = %v", st.OpeningBalance)
	}
	if _, err := (importer{}).Parse(context.Background(), "x.csv", []byte("a,b\n1,2\n")); err == nil {
		t.Fatal("a file in another format must be refused")
	}
	if !(importer{}).Detect("X.CSV", []byte(data)) || (importer{}).Detect("x.csv", []byte("a,b")) || (importer{}).Detect("x.txt", []byte(data)) {
		t.Fatal("Detect")
	}
}

func TestEvenPolicySumsToTotal(t *testing.T) {
	units := []sdk.AllocationUnit{{UnitID: "a"}, {UnitID: "b"}, {UnitID: "c"}}
	out, err := evenPolicy{}.Allocate(context.Background(), sdk.AllocationRequest{Total: sdk.Money{Amount: 1000, Currency: "TRY"}, Units: units})
	if err != nil {
		t.Fatal(err)
	}
	var sum int64
	for _, u := range out {
		sum += u.Amount
	}
	if sum != 1000 || out[0].Amount != 334 || out[2].Amount != 333 {
		t.Fatalf("allocation = %+v", out)
	}
}

func TestNewestFirstOrder(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 1, d, 0, 0, 0, 0, time.UTC) }
	ids, err := newestFirst{}.Order(context.Background(), []sdk.OpenItem{
		{ID: "old", DueOn: day(1), Kind: "principal"}, {ID: "new-fee", DueOn: day(9), Kind: "fee"}, {ID: "new", DueOn: day(9), Kind: "principal"},
	})
	if err != nil || len(ids) != 3 || ids[0] != "new-fee" || ids[1] != "new" || ids[2] != "old" {
		t.Fatalf("order = %v, %v", ids, err)
	}
}

func TestWebhookSignature(t *testing.T) {
	body := []byte(`{"event":"ping"}`)
	if !verify("secret", body, Sign("secret", body)) || verify("secret", body, Sign("other", body)) || verify("secret", append(body, ' '), Sign("secret", body)) {
		t.Fatal("signature verification")
	}
}

func TestTaxID(t *testing.T) {
	if taxID.Validate(taxID{}, "12345678") != nil || taxID.Validate(taxID{}, "1234567") == nil || taxID.Validate(taxID{}, "1234567a") == nil {
		t.Fatal("tax id validation")
	}
}

func TestSettingsSchemasAreNamespaced(t *testing.T) {
	for _, scope := range []sdk.ScopeKind{sdk.ScopeOrg, sdk.ScopeSite} {
		if len(settings{}.Schema(scope)) == 0 {
			t.Errorf("no schema for %s scope", scope)
		}
	}
}
