package authz

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/problem"
)

type fakeLoader struct {
	org, site uuid.UUID
	blocks    map[uuid.UUID]bool
}

func (f fakeLoader) SiteOrg(_ context.Context, _ pgx.Tx, id uuid.UUID) (uuid.UUID, error) {
	if id != f.site {
		return uuid.Nil, ErrScopeNotFound
	}
	return f.org, nil
}

func (f fakeLoader) BlockParents(_ context.Context, _ pgx.Tx, id uuid.UUID) (uuid.UUID, uuid.UUID, error) {
	if !f.blocks[id] {
		return uuid.Nil, uuid.Nil, ErrScopeNotFound
	}
	return f.org, f.site, nil
}

func TestScopeOfDefaultsToUnavailable(t *testing.T) {
	SetScopeLoader(nil)
	_, err := ScopeOfSite(context.Background(), nil, uuid.New())
	if p := problem.From(err); p.Status != 500 || !errors.Is(err, ErrScopeUnavailable) {
		t.Errorf("ScopeOfSite without a loader: %v", err)
	}
	if _, err := ScopeOfBlock(context.Background(), nil, uuid.New()); !errors.Is(err, ErrScopeUnavailable) {
		t.Errorf("ScopeOfBlock without a loader: %v", err)
	}
}

func TestScopeOfPopulatesParentsFromStorage(t *testing.T) {
	org, site, block := uuid.New(), uuid.New(), uuid.New()
	SetScopeLoader(fakeLoader{org: org, site: site, blocks: map[uuid.UUID]bool{block: true}})
	t.Cleanup(func() { SetScopeLoader(nil) })
	ctx := context.Background()

	s, err := ScopeOfSite(ctx, nil, site)
	if err != nil || s.OrgID != org || s.SiteID == nil || *s.SiteID != site || s.BlockID != nil {
		t.Fatalf("ScopeOfSite = %+v, %v", s, err)
	}
	b, err := ScopeOfBlock(ctx, nil, block)
	if err != nil || b.OrgID != org || b.SiteID == nil || *b.SiteID != site || b.BlockID == nil || *b.BlockID != block {
		t.Fatalf("ScopeOfBlock = %+v, %v", b, err)
	}
	// a site grant covers the block derived from storage, another site's grant does not
	g := Grants{OrgID: org, Items: []Grant{{Permission: "x.y", ScopeType: ScopeSite, ScopeID: &site}}}
	if !g.Has("x.y", b) {
		t.Error("a site grant must cover the block of that site")
	}
	other := uuid.New()
	g = Grants{OrgID: org, Items: []Grant{{Permission: "x.y", ScopeType: ScopeSite, ScopeID: &other}}}
	if g.Has("x.y", b) {
		t.Error("a grant of another site must not cover the block")
	}
	// unknown ids are 404 problems
	if _, err := ScopeOfSite(ctx, nil, uuid.New()); problem.From(err).Status != 404 {
		t.Errorf("unknown site: %v", err)
	}
	if _, err := ScopeOfBlock(ctx, nil, uuid.New()); problem.From(err).Status != 404 {
		t.Errorf("unknown block: %v", err)
	}
}

// scopeLiterals returns the positions of authz.Scope composite literals that
// set SiteID or BlockID (keyed or positional) in the Go source src, where the
// package is imported as authz (or, inside package authz itself, as Scope).
func scopeLiterals(t *testing.T, fset *token.FileSet, f *ast.File, inAuthz bool) []token.Position {
	t.Helper()
	var out []token.Position
	ast.Inspect(f, func(n ast.Node) bool {
		cl, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		isScope := false
		switch ty := cl.Type.(type) {
		case *ast.SelectorExpr:
			if x, ok := ty.X.(*ast.Ident); ok && x.Name == "authz" && ty.Sel.Name == "Scope" {
				isScope = true
			}
		case *ast.Ident:
			isScope = inAuthz && ty.Name == "Scope"
		}
		if !isScope {
			return true
		}
		positional := len(cl.Elts) > 0
		flagged := false
		for _, e := range cl.Elts {
			if kv, ok := e.(*ast.KeyValueExpr); ok {
				positional = false
				if k, ok := kv.Key.(*ast.Ident); ok && (k.Name == "SiteID" || k.Name == "BlockID") {
					flagged = true
				}
			}
		}
		if flagged || positional && len(cl.Elts) > 1 {
			out = append(out, fset.Position(cl.Pos()))
		}
		return true
	})
	return out
}

func TestScopeLiteralLintCatchesForgery(t *testing.T) {
	src := `package x
import "github.com/openbekci/openbekci/server/internal/core/authz"
func f(site, block *uuid) {
	_ = authz.Scope{OrgID: o}
	_ = authz.Scope{OrgID: o, SiteID: site}
	_ = authz.Scope{OrgID: o, SiteID: site, BlockID: block}
	_ = authz.Scope{o, site, block}
}`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(scopeLiterals(t, fset, f, false)); got != 3 {
		t.Errorf("lint found %d forged literals, want 3 (the org-only literal is fine)", got)
	}
}

// TestNoScopeLiteralsOutsideAuthz forbids authz.Scope{...SiteID/BlockID...}
// literals in non-test code outside internal/core/authz: a site or block scope
// is built by ScopeOfSite / ScopeOfBlock from the stored row. An org-only
// Scope{OrgID: ...} literal is fine.
func TestNoScopeLiteralsOutsideAuthz(t *testing.T) {
	server := filepath.Join("..", "..", "..")
	self, _ := filepath.Abs(".")
	fset := token.NewFileSet()
	err := filepath.WalkDir(server, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if abs, _ := filepath.Abs(path); abs == self || d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		for _, pos := range scopeLiterals(t, fset, f, false) {
			t.Errorf("%s: authz.Scope literal with SiteID or BlockID; derive it with authz.ScopeOfSite or authz.ScopeOfBlock (the scope must come from the stored row, not from input)", pos)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
