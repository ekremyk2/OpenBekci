package privacy

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/devices"
	"github.com/openbekci/openbekci/server/internal/core/people"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// EraseResult says what an erase did.
type EraseResult struct {
	// OpenItems is the number of open charges the person owed or shared when it
	// was forced through (zero otherwise).
	OpenItems int
}

// Erase erases a person for a privacy request. It needs people.erase
// organization-wide and a recent re-authentication. force erases although the
// person has open charges (audited as forced).
func (s *Service) Erase(ctx context.Context, orgID, personID uuid.UUID, reason string, force bool) (EraseResult, error) {
	if err := authz.Require(ctx, people.PermPersonErase, authz.Scope{OrgID: orgID}); err != nil {
		return EraseResult{}, err
	}
	if err := authz.PrincipalFromContext(ctx).RequireReauth(s.now()); err != nil {
		return EraseResult{}, err
	}
	return s.erase(ctx, orgID, personID, reason, force)
}

// EraseAsOperator is Erase for the operator's command line: the caller is the
// person running `openbekci admin privacy erase` on the server, so there is no
// principal to check; the event is audited as a system event.
func (s *Service) EraseAsOperator(ctx context.Context, orgID, personID uuid.UUID, reason string, force bool) (EraseResult, error) {
	return s.erase(ctx, orgID, personID, reason, force)
}

// erase pseudonymises the person: the row keeps its id, so ledger lines,
// ballots, attendance and the audit trail (whose diffs hold only keyed hashes)
// stay as they are and now point at "Former person xxxxxxxx"; every personal
// column, the sign-in link, sessions, devices and consents go.
//
// shortcut: it runs on the platform role, because the account (a user row
// shared by every organization of that human) is scrubbed only when no person
// of any organization is left linked to it, and RLS hides the other
// organizations; every statement filters org_id. Not covered: free text the
// person wrote (tickets, comments), vehicles of their unit and uploaded files
// stay with the site, and backups age out (see the retention table in the
// server README).
func (s *Service) erase(ctx context.Context, orgID, personID uuid.UUID, reason string, force bool) (EraseResult, error) {
	var res EraseResult
	err := s.db.InPlatformTx(ctx, func(tx pgx.Tx) error {
		var userID *uuid.UUID
		var siteID *uuid.UUID
		var erased bool
		err := tx.QueryRow(ctx, `SELECT user_id, site_id, erased_at IS NOT NULL FROM person WHERE org_id = $1 AND id = $2 FOR UPDATE`, orgID, personID).Scan(&userID, &siteID, &erased)
		if isNoRows(err) {
			return problem.NotFound("no such person")
		}
		if err != nil {
			return err
		}
		if erased {
			return problem.Conflict("the person is already erased")
		}
		if err := tx.QueryRow(ctx, openItemsSQL, orgID, personID).Scan(&res.OpenItems); err != nil {
			return err
		}
		if res.OpenItems > 0 && !force {
			return problem.Conflict(fmt.Sprintf("the person has %d open charges; settle them or erase with the override", res.OpenItems))
		}
		for _, q := range []string{
			`UPDATE person SET display_name = 'Former person ' || left(id::text, 8), contact = '{}', gate_contact_phone = NULL, notes = NULL,
				national_id_enc = NULL, national_id_country = NULL, user_id = NULL, erased_at = now(), updated_at = now(), version = version + 1
				WHERE org_id = $1 AND id = $2`,
			`DELETE FROM directory_consent WHERE org_id = $1 AND person_id = $2`,
			`DELETE FROM notification_mute WHERE org_id = $1 AND person_id = $2`,
			`UPDATE invitation SET email = NULL, phone = NULL, status = CASE WHEN status = 'pending' THEN 'revoked' ELSE status END, updated_at = now()
				WHERE org_id = $1 AND person_id = $2`,
			`UPDATE unit_claim SET message = NULL, decision_note = NULL WHERE org_id = $1 AND person_id = $2`,
			// an erased person holds no role
			`UPDATE membership SET valid_to = GREATEST(now(), valid_from + interval '1 second'), updated_at = now()
				WHERE org_id = $1 AND person_id = $2 AND (valid_to IS NULL OR valid_to > now())`,
		} {
			if _, err := tx.Exec(ctx, q, orgID, personID); err != nil {
				return err
			}
		}
		if userID != nil {
			if err := s.dropAccount(ctx, tx, orgID, *userID); err != nil {
				return err
			}
		}
		diff, err := audit.Diff("privacy_erase", nil, map[string]any{"reason": reason, "forced": force, "open_items": res.OpenItems})
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: siteID, Action: "privacy.erase", TargetType: "person", TargetID: personID, Diff: diff})
	})
	if err != nil {
		return EraseResult{}, tenancy.DBProblem(err, "erase person")
	}
	return res, nil
}

// dropAccount ends the sign-in of the erased person's user: sessions, devices
// and the consents of this organization go; when no person of any
// organization is linked to the account any more, the account itself is
// disabled and scrubbed (a platform administrator's account is left alone).
func (s *Service) dropAccount(ctx context.Context, tx pgx.Tx, orgID, userID uuid.UUID) error {
	if err := devices.DeleteForUser(ctx, tx, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM consent WHERE org_id = $1 AND user_id = $2`, orgID, userID); err != nil {
		return err
	}
	for _, q := range []string{
		`DELETE FROM session WHERE user_id = $1`,
		`DELETE FROM web_login_link WHERE user_id = $1`,
		`UPDATE "user" SET status = 'disabled', email = 'erased-' || id::text || '@erased.invalid', phone = NULL, password_hash = NULL, display_name = NULL,
			updated_at = now(), version = version + 1
			WHERE id = $1 AND NOT is_platform_admin AND NOT EXISTS (SELECT 1 FROM person WHERE user_id = $1)`,
	} {
		if _, err := tx.Exec(ctx, q, userID); err != nil {
			return err
		}
	}
	return nil
}

// openItemsSQL counts the open charges of a person: those they owe directly,
// and those of the unit sides they currently own or rent.
const openItemsSQL = `SELECT count(*) FROM charge c WHERE c.org_id = $1 AND c.status IN ('open', 'partially_paid') AND (
	c.debtor_person_id = $2 OR (c.debtor_kind IN ('owner', 'tenant') AND EXISTS (
		SELECT 1 FROM occupancy o WHERE o.org_id = c.org_id AND o.unit_id = c.unit_id AND o.person_id = $2 AND o.kind <> 'household'
			AND o.side = c.debtor_kind AND o.valid_from <= current_date AND (o.valid_to IS NULL OR o.valid_to > current_date))))`
