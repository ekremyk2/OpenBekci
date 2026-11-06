package identity_test

import (
	"context"
	"testing"

	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/core/platformadmin"
)

func createOrg(t *testing.T, e *env, slug, email string) (platformadmin.Org, error) {
	t.Helper()
	resolve, err := e.svc.PrepareFindOrCreateUser(email, identity.CreateUserParams{Email: email})
	if err != nil {
		t.Fatal(err)
	}
	return e.admin.CreateOrganization(context.Background(), platformadmin.CreateOrgParams{
		Slug: slug, Name: "Test " + slug, ResolveAdmin: resolve, AdminName: email,
	})
}

// `admin create-org` is all or nothing: a failure after the user was created
// (here: the slug is taken) leaves no user behind, and a rerun with a free
// slug creates everything.
func TestIntegrationCreateOrgIsTransactional(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	first, err := createOrg(t, e, "gul", "first@example.org")
	if err != nil || !first.AdminCreated {
		t.Fatalf("first org: %+v, %v", first, err)
	}
	if _, found, _ := e.svc.FindUser(ctx, "first@example.org"); !found {
		t.Fatal("the new administrator was not created")
	}

	// slug conflict: the would-be new user must not survive
	if _, err := createOrg(t, e, "gul", "bob@example.org"); err == nil {
		t.Fatal("duplicate slug accepted")
	}
	if _, found, _ := e.svc.FindUser(ctx, "bob@example.org"); found {
		t.Fatal("a user was left behind by a failed create-org")
	}

	// a bad organization (invalid slug) fails before anything is written
	if _, err := createOrg(t, e, "Not A Slug", "bob@example.org"); err == nil {
		t.Fatal("invalid slug accepted")
	}
	if _, found, _ := e.svc.FindUser(ctx, "bob@example.org"); found {
		t.Fatal("a user was left behind by an invalid create-org")
	}

	// the rerun with a free slug works, and an existing user becomes admin of a second org
	bob, err := createOrg(t, e, "gul-2", "bob@example.org")
	if err != nil || !bob.AdminCreated {
		t.Fatalf("rerun: %+v, %v", bob, err)
	}
	again, err := createOrg(t, e, "gul-3", "bob@example.org")
	if err != nil || again.AdminCreated || again.AdminUserID != bob.AdminUserID {
		t.Fatalf("existing user: %+v, %v", again, err)
	}
	// either way a login link can be issued and works
	link, err := e.svc.CreateLoginLink(ctx, again.AdminUserID, identity.ClientWeb)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := e.svc.MagicLinkSignIn(ctx, tokenOf(t, link), meta); err != nil || got.ID != bob.AdminUserID {
		t.Fatalf("link of the existing administrator: %v", err)
	}
}
