// Package packdocs renders a document with a template of a pack enabled for the
// organization and typesets it as a PDF, for the core modules whose generic
// documents a pack may replace (money documents, statements, the ihtarname).
// Governance does the same for its own documents. A module renders its generic
// document when Render says there is no pack template (or the template failed).
package packdocs

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/governance"
	"github.com/openbekci/openbekci/server/internal/platform/doctpl"
)

// Render asks the resolver for a template of kind and typesets its output.
// ok is false when there is no resolver, no enabled pack has a template of the
// kind for the locale, or the template or the typesetting failed (logged): the
// caller then renders its generic document.
func Render(ctx context.Context, r doctpl.Resolver, log *slog.Logger, org uuid.UUID, kind, locale string, data map[string]any) (pdf []byte, ok bool) {
	if r == nil {
		return nil, false
	}
	doc, found, err := r.Render(ctx, org, kind, locale, data)
	if err != nil || !found {
		if err != nil && log != nil {
			log.WarnContext(ctx, "pack document template failed; the generic document is used", "kind", kind, "error", err)
		}
		return nil, false
	}
	if !doc.IsMarkdown() {
		return doc.Body, true
	}
	pdf, err = governance.ModelFromMarkdown(string(doc.Body), nil).PDF()
	if err != nil {
		if log != nil {
			log.WarnContext(ctx, "pack document could not be typeset; the generic document is used", "kind", kind, "error", err)
		}
		return nil, false
	}
	return pdf, true
}

// OrgLocale is the default locale of the organization.
func OrgLocale(ctx context.Context, tx pgx.Tx, org uuid.UUID) string {
	var locale string
	if err := tx.QueryRow(ctx, `SELECT default_locale FROM organization WHERE id = $1`, org).Scan(&locale); err != nil {
		return ""
	}
	return locale
}
