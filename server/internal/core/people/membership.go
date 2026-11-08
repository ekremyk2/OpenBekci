package people

import (
	"context"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Membership is a role grant of a person, as the API shows it.
type Membership struct {
	ID         uuid.UUID  `json:"id"`
	PersonID   uuid.UUID  `json:"person_id"`
	PersonName string     `json:"person_name"`
	Role       string     `json:"role"`
	ScopeType  string     `json:"scope_type"`
	ScopeID    *uuid.UUID `json:"scope_id"`
	ValidFrom  time.Time  `json:"valid_from"`
	ValidTo    *time.Time `json:"valid_to"`
	SiteOwned  bool       `json:"site_owned"`
	DecisionID *uuid.UUID `json:"decision_id"`
	Current    bool       `json:"current"`
	Version    int        `json:"version"`
	CreatedAt  time.Time  `json:"created_at"`
}

const membershipSelect = `SELECT m.id, m.person_id, p.display_name, m.role, m.scope_type, m.scope_id, m.valid_from, m.valid_to, m.site_owned, m.decision_id,
	m.valid_from <= now() AND (m.valid_to IS NULL OR m.valid_to > now()), m.version, m.created_at
	FROM membership m JOIN person p ON p.org_id = m.org_id AND p.id = m.person_id`

func scanMembership(row pgx.Row) (Membership, error) {
	var m Membership
	err := row.Scan(&m.ID, &m.PersonID, &m.PersonName, &m.Role, &m.ScopeType, &m.ScopeID, &m.ValidFrom, &m.ValidTo, &m.SiteOwned, &m.DecisionID, &m.Current, &m.Version, &m.CreatedAt)
	m.ValidFrom, m.CreatedAt = m.ValidFrom.UTC(), m.CreatedAt.UTC()
	if m.ValidTo != nil {
		t := m.ValidTo.UTC()
		m.ValidTo = &t
	}
	return m, err
}

// ListMemberships lists role grants, optionally of one person or one site. It
// needs membership.read (organization-wide, or for the sites the grants are scoped to).
func (s *Service) ListMemberships(ctx context.Context, orgID uuid.UUID, personID, siteID *uuid.UUID, p tenancy.PageParams) ([]Membership, string, error) {
	if err := authz.RequireAnywhere(ctx, PermMembershipRead); err != nil {
		return nil, "", err
	}
	limit, after, err := p.Resolve()
	if err != nil {
		return nil, "", err
	}
	orgWide, sites := siteGrants(ctx, PermMembershipRead)
	var out []Membership
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if siteID != nil {
			if _, err := authz.ScopeOfSite(ctx, tx, *siteID); err != nil {
				return err
			}
		}
		rows, err := tx.Query(ctx, membershipSelect+` WHERE m.org_id = $1 AND m.id > $2
			AND ($3 OR (m.scope_type = 'site' AND m.scope_id = ANY($4))
			        OR (m.scope_type = 'block' AND EXISTS (SELECT 1 FROM block b WHERE b.org_id = m.org_id AND b.id = m.scope_id AND b.site_id = ANY($4))))
			AND ($5::uuid IS NULL OR m.person_id = $5)
			AND ($6::uuid IS NULL OR (m.scope_type = 'site' AND m.scope_id = $6)
			        OR (m.scope_type = 'block' AND EXISTS (SELECT 1 FROM block b WHERE b.org_id = m.org_id AND b.id = m.scope_id AND b.site_id = $6)))
			ORDER BY m.id LIMIT $7`, orgID, after, orgWide, sites, personID, siteID, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			m, err := scanMembership(rows)
			if err != nil {
				return err
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", tenancy.DBProblem(err, "list memberships")
	}
	items, next := tenancy.Page(out, limit, func(m Membership) uuid.UUID { return m.ID })
	return items, next, nil
}

type membershipAudit struct {
	PersonID   uuid.UUID  `json:"person_id"`
	Role       string     `json:"role"`
	ScopeType  string     `json:"scope_type"`
	ScopeID    *uuid.UUID `json:"scope_id"`
	ValidFrom  time.Time  `json:"valid_from"`
	ValidTo    *time.Time `json:"valid_to"`
	SiteOwned  bool       `json:"site_owned"`
	DecisionID *uuid.UUID `json:"decision_id"`
}

func (m Membership) audit() membershipAudit {
	return membershipAudit{m.PersonID, m.Role, m.ScopeType, m.ScopeID, m.ValidFrom, m.ValidTo, m.SiteOwned, m.DecisionID}
}

// memberScope derives the authorization scope of a membership scope from the
// site or block row.
func memberScope(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, scopeType string, scopeID *uuid.UUID) (authz.Scope, error) {
	switch scopeType {
	case "org":
		return authz.Scope{OrgID: orgID}, nil
	case "site":
		if scopeID == nil {
			return authz.Scope{}, fieldProblem("scope_id", "required")
		}
		return authz.ScopeOfSite(ctx, tx, *scopeID)
	case "block":
		if scopeID == nil {
			return authz.Scope{}, fieldProblem("scope_id", "required")
		}
		return authz.ScopeOfBlock(ctx, tx, *scopeID)
	}
	return authz.Scope{}, fieldProblem("scope_type", "invalid_value")
}

// CreateMembership grants a role to a person. It needs membership.manage for
// the scope (organization-wide to grant org_admin or an organization-scoped
// membership). board_member and auditor memberships are site-owned (D2) and
// must be scoped to a site or block.
func (s *Service) CreateMembership(ctx context.Context, orgID uuid.UUID, d Doc) (Membership, error) {
	if err := authz.RequireAnywhere(ctx, PermMembershipManage); err != nil {
		return Membership{}, err
	}
	fields := d.Unknown("person_id", "role", "scope_type", "scope_id", "valid_from", "valid_to", "decision_id")
	var personID uuid.UUID
	var role, scopeType string
	var scopeID, decisionID *uuid.UUID
	if !d.Has("person_id") {
		fields = append(fields, problem.Field{Field: "person_id", Code: "required"})
	} else if err := d.Get("person_id", &personID); err != nil {
		return Membership{}, err
	}
	if !d.Has("role") {
		fields = append(fields, problem.Field{Field: "role", Code: "required"})
	} else if err := d.Get("role", &role); err != nil {
		return Membership{}, err
	} else if !slices.Contains(authz.BuiltinRoleNames(), role) {
		fields = append(fields, problem.Field{Field: "role", Code: "invalid_value"})
	}
	scopeType = "org"
	if err := d.Get("scope_type", &scopeType); err != nil {
		return Membership{}, err
	}
	if d.Has("scope_id") && !d.IsNull("scope_id") {
		var v uuid.UUID
		if err := d.Get("scope_id", &v); err != nil {
			return Membership{}, err
		}
		scopeID = &v
	}
	if d.Has("decision_id") && !d.IsNull("decision_id") {
		var v uuid.UUID
		if err := d.Get("decision_id", &v); err != nil {
			return Membership{}, err
		}
		decisionID = &v
	}
	from := s.now().UTC()
	if d.Has("valid_from") {
		if err := d.Get("valid_from", &from); err != nil {
			return Membership{}, err
		}
	}
	var to *time.Time
	if d.Has("valid_to") && !d.IsNull("valid_to") {
		var v time.Time
		if err := d.Get("valid_to", &v); err != nil {
			return Membership{}, err
		}
		to = &v
		if !v.After(from) {
			fields = append(fields, problem.Field{Field: "valid_to", Code: "invalid_value"})
		}
	}
	if (scopeType == "org") != (scopeID == nil) {
		fields = append(fields, problem.Field{Field: "scope_id", Code: map[bool]string{true: "required", false: "invalid_value"}[scopeType != "org"]})
	}
	siteOwned := role == string(authz.RoleBoardMember) || role == string(authz.RoleAuditor)
	if siteOwned && scopeType == "org" {
		fields = append(fields, problem.Field{Field: "scope_type", Code: "invalid_value", Params: map[string]any{"reason": "site_owned_roles_belong_to_a_site"}})
	}
	if len(fields) > 0 {
		return Membership{}, problem.Validation(fields...)
	}
	var out Membership
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		sc, err := memberScope(ctx, tx, orgID, scopeType, scopeID)
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermMembershipManage, sc); err != nil {
			return err
		}
		if role == string(authz.RoleOrgAdmin) && !authz.Has(ctx, PermMembershipManage, authz.Scope{OrgID: orgID}) {
			return problem.Forbidden("only an organization-wide manager can grant org_admin")
		}
		if sc.SiteID != nil {
			if _, err := tenancy.LoadSite(ctx, tx, orgID, *sc.SiteID, false, false); err != nil {
				return err
			}
		}
		mid := id.New()
		if _, err := tx.Exec(ctx, `INSERT INTO membership (id, org_id, person_id, role, scope_type, scope_id, valid_from, valid_to, site_owned, decision_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`, mid, orgID, personID, role, scopeType, scopeID, from, to, siteOwned, decisionID); err != nil {
			return err
		}
		if out, err = scanMembership(tx.QueryRow(ctx, membershipSelect+` WHERE m.org_id = $1 AND m.id = $2`, orgID, mid)); err != nil {
			return err
		}
		diff, err := audit.Diff("membership", nil, out.audit())
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: sc.SiteID, Action: "membership.create", TargetType: "membership", TargetID: mid, Diff: diff})
	})
	if err != nil {
		return Membership{}, tenancy.DBProblem(err, "membership")
	}
	return out, nil
}

// EndMembership ends a membership now (the row stays, end-dated). Ending a
// site-owned membership (board member, auditor) is an audited override that
// needs a recent re-authentication; the last organization administrator cannot be ended.
func (s *Service) EndMembership(ctx context.Context, orgID, membershipID uuid.UUID) error {
	if err := authz.RequireAnywhere(ctx, PermMembershipManage); err != nil {
		return err
	}
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		m, err := scanMembership(tx.QueryRow(ctx, membershipSelect+` WHERE m.org_id = $1 AND m.id = $2 FOR UPDATE OF m`, orgID, membershipID))
		if err != nil {
			return err
		}
		sc, err := memberScope(ctx, tx, orgID, m.ScopeType, m.ScopeID)
		if err != nil {
			return err
		}
		if err := authz.Require(ctx, PermMembershipManage, sc); err != nil {
			return err
		}
		if !m.Current {
			return problem.Conflict("this membership has already ended")
		}
		if m.SiteOwned {
			p := authz.PrincipalFromContext(ctx)
			if err := p.RequireReauth(s.now()); err != nil {
				return err
			}
		}
		if m.Role == string(authz.RoleOrgAdmin) && m.ScopeType == "org" {
			// serialize concurrent removals of administrators of this organization (M5)
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('org_admin:' || $1::text, 0))`, orgID); err != nil {
				return err
			}
			var others int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM membership WHERE org_id = $1 AND role = 'org_admin' AND scope_type = 'org' AND id <> $2
				AND valid_from <= now() AND (valid_to IS NULL OR valid_to > now())`, orgID, membershipID).Scan(&others); err != nil {
				return err
			}
			if others == 0 {
				return problem.Conflict("the last organization administrator cannot be removed")
			}
		}
		now := s.now().UTC()
		if _, err := tx.Exec(ctx, `UPDATE membership SET valid_to = GREATEST($3::timestamptz, valid_from + interval '1 microsecond'), updated_at = now(), version = version + 1
			WHERE org_id = $1 AND id = $2`, orgID, membershipID, now); err != nil {
			return err
		}
		after := m
		after.ValidTo = &now
		diff, err := audit.Diff("membership", m.audit(), after.audit())
		if err != nil {
			return err
		}
		action := "membership.end"
		if m.SiteOwned {
			action = "membership.end_site_owned" // override with re-authentication
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: sc.SiteID, Action: action, TargetType: "membership", TargetID: membershipID, Diff: diff})
	})
	return tenancy.DBProblem(err, "end membership")
}
