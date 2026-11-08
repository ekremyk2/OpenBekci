// Package doctpl is the seam between the core's document renderers and the
// document templates of packs. A renderer (governance, money documents,
// collections) builds the data of a document, asks the Resolver whether a pack
// enabled for the organization has a template of that kind for the locale, and
// typesets the template's output; without one it renders its generic
// document. The core never imports a pack: the wiring hands the renderers a
// Resolver backed by the pack host.
package doctpl

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
)

// Document kinds the core asks for. A pack may also supply templates of other
// kinds; the three marked pack-only have no generic equivalent in the core and
// are rendered only when an enabled pack provides them.
const (
	KindCallLetter        = "call_letter"
	KindAttendanceList    = "attendance_list"
	KindMinutes           = "minutes"
	KindDecisionList      = "decision_list"
	KindReceipt           = "receipt"
	KindStatement         = "statement"
	KindClosingStatement  = "closing_statement"
	KindDebtCertificate   = "debt_certificate"
	KindDemandLetter      = "demand_letter"
	KindDeliveryChecklist = "delivery_checklist" // pack-only
	KindProxyForm         = "proxy_form"         // pack-only
	KindDecisionBookDraft = "decision_book_draft" // pack-only
)

// Doc is a rendered pack document: Markdown (MIME "text/markdown", typeset by
// the core) or a finished file (any other MIME).
type Doc struct {
	MIME string
	Body []byte
}

// IsMarkdown reports whether the document is the Markdown subset the core typesets.
func (d Doc) IsMarkdown() bool { return strings.HasPrefix(d.MIME, "text/markdown") }

// Resolver finds and runs the template of a kind among the packs enabled for an organization.
type Resolver interface {
	// Render renders kind in locale with data. ok is false when no enabled pack
	// has a template of that kind for the locale; the caller renders its own.
	Render(ctx context.Context, org uuid.UUID, kind, locale string, data map[string]any) (doc Doc, ok bool, err error)
}

// ToData converts a value to the JSON-shaped data templates receive (maps,
// slices, strings, float64 and bool).
func ToData(v any) map[string]any {
	raw, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}
