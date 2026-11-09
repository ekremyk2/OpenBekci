package people

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// JoinRef names the site behind a valid join code.
type JoinRef struct {
	OrgID, SiteID uuid.UUID
}

// CheckJoinCode answers whether code is a join code that can still be used,
// and for which site. An unknown, malformed, disabled, rotated or exhausted
// code answers the same 404 as the join read. It is the gate of the join-code
// sign-up, which needs no account: the code on the site's notice board is the
// secret, and the address limit is the one of the join read.
func (s *Service) CheckJoinCode(ctx context.Context, code string) (JoinRef, error) {
	if err := allow(s.lim.previewIP, clientIP(ctx)); err != nil {
		return JoinRef{}, err
	}
	orgID, ok := parseScopedToken(code)
	if !ok {
		return JoinRef{}, problem.NotFound("this join code is not valid")
	}
	var ref JoinRef
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		siteID, _, err := s.findJoinCode(ctx, tx, orgID, code)
		ref = JoinRef{OrgID: orgID, SiteID: siteID}
		return err
	})
	if err != nil {
		return JoinRef{}, tenancy.DBProblem(err, "join code")
	}
	return ref, nil
}

// AuditJoinSignUp records, against the site, that someone asked to sign up
// through its join code. It is called after the request was accepted, so
// refused requests do not fill the log. It records the channel only, never the
// phone number or address, and is the same for a new and an existing contact
// (the log must not tell them apart either).
func (s *Service) AuditJoinSignUp(ctx context.Context, ref JoinRef, channel string) error {
	diff, err := audit.Diff("join_signup", nil, map[string]any{"channel": channel})
	if err != nil {
		return problem.Internal(err)
	}
	err = s.db.InTenantTx(ctx, ref.OrgID, func(tx pgx.Tx) error {
		return audit.Record(ctx, tx, audit.Event{OrgID: ref.OrgID, SiteID: &ref.SiteID, ActorKind: audit.ActorSystem,
			Action: "join.sign_up", TargetType: "site", TargetID: ref.SiteID, Diff: diff})
	})
	if err != nil {
		return tenancy.DBProblem(err, "audit join sign-up")
	}
	return nil
}
