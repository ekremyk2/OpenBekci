package access

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/core/access/protocol"
	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/authz"
	"github.com/openbekci/openbekci/server/internal/platform/id"
	"github.com/openbekci/openbekci/server/internal/problem"
)

// Credential is a credential as the API shows it (derived, read-only).
type Credential struct {
	ID             uuid.UUID           `json:"id"`
	OrgID          uuid.UUID           `json:"org_id"`
	SiteID         uuid.UUID           `json:"site_id"`
	Kind           string              `json:"kind"`
	Value          string              `json:"value"`
	SourceType     string              `json:"source_type"`
	SourceID       *uuid.UUID          `json:"source_id"`
	UnitID         *uuid.UUID          `json:"unit_id"`
	UnitDoorNumber *string             `json:"unit_door_number"`
	PersonID       *uuid.UUID          `json:"person_id"`
	PointScope     string              `json:"point_scope"`
	PointIDs       []uuid.UUID         `json:"point_ids"`
	ValidFrom      time.Time           `json:"valid_from"`
	ValidTo        *time.Time          `json:"valid_to"`
	Recurring      *protocol.Recurring `json:"recurring"`
	MaxUses        *int                `json:"max_uses"`
	Uses           int                 `json:"uses"`
	Status         string              `json:"status"`
	StatusReason   *string             `json:"status_reason"`
	CreatedAt      time.Time           `json:"created_at"`
	UpdatedAt      time.Time           `json:"updated_at"`
}

const viewColumns = `c.id, c.org_id, c.site_id, c.kind, c.value, c.source_type, c.source_id, c.unit_id, u.door_number, c.person_id,
	c.point_scope, c.point_ids, c.valid_from, c.valid_to, c.recurring, c.max_uses, c.uses, c.status, c.status_reason, c.created_at, c.updated_at`

const viewFrom = ` FROM credential c LEFT JOIN unit u ON u.org_id = c.org_id AND u.id = c.unit_id`

func scanCredential(row pgx.Row) (Credential, error) {
	var c Credential
	var rec []byte
	err := row.Scan(&c.ID, &c.OrgID, &c.SiteID, &c.Kind, &c.Value, &c.SourceType, &c.SourceID, &c.UnitID, &c.UnitDoorNumber, &c.PersonID,
		&c.PointScope, &c.PointIDs, &c.ValidFrom, &c.ValidTo, &rec, &c.MaxUses, &c.Uses, &c.Status, &c.StatusReason, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return c, err
	}
	if len(rec) > 0 && string(rec) != "null" {
		var r protocol.Recurring
		if json.Unmarshal(rec, &r) == nil {
			c.Recurring = &r
		}
	}
	if c.PointIDs == nil {
		c.PointIDs = []uuid.UUID{}
	}
	c.ValidFrom = c.ValidFrom.UTC()
	if c.ValidTo != nil {
		t := c.ValidTo.UTC()
		c.ValidTo = &t
	}
	c.CreatedAt, c.UpdatedAt = c.CreatedAt.UTC(), c.UpdatedAt.UTC()
	return c, nil
}

// CredentialFilter narrows ListCredentials.
type CredentialFilter struct {
	Kind, Status, SourceType, Query string
	UnitID, PointID                 *uuid.UUID
}

// ListCredentials lists the derived credentials of a site (permission access.read).
func (s *Service) ListCredentials(ctx context.Context, orgID, siteID uuid.UUID, f CredentialFilter, limit int, cursor string) ([]Credential, string, error) {
	if err := authz.RequireInOrg(ctx, PermRead, orgID); err != nil {
		return nil, "", err
	}
	after, err := decodeIDCursor(cursor)
	if err != nil {
		return nil, "", err
	}
	n := pageSize(limit)
	q := NormalizePlate(f.Query)
	var out []Credential
	err = s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		if err := siteScope(ctx, tx, PermRead, siteID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+viewColumns+viewFrom+` WHERE c.org_id = $1 AND c.site_id = $2 AND c.id > $3
			AND ($4 = '' OR c.kind = $4) AND ($5 = '' OR c.status = $5) AND ($6 = '' OR c.source_type = $6)
			AND ($7::uuid IS NULL OR c.unit_id = $7)
			AND ($8::uuid IS NULL OR c.point_scope = 'all' OR (c.point_scope = 'ids' AND $8 = ANY(c.point_ids))
			     OR (c.point_scope = 'all_vehicle' AND EXISTS (SELECT 1 FROM access_point p WHERE p.org_id = c.org_id AND p.id = $8 AND p.kind = 'vehicle_gate'))
			     OR (c.point_scope = 'all_pedestrian' AND EXISTS (SELECT 1 FROM access_point p WHERE p.org_id = c.org_id AND p.id = $8 AND p.kind = 'pedestrian_door')))
			AND ($9 = '' OR c.value LIKE '%' || $9 || '%' OR u.door_number ILIKE '%' || $10 || '%')
			ORDER BY c.id LIMIT $11`,
			orgID, siteID, after, f.Kind, f.Status, f.SourceType, f.UnitID, f.PointID, q, f.Query, n+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanCredential(rows)
			if err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", dbProblem(err, "credentials")
	}
	items, next := pageOf(out, n, func(c Credential) uuid.UUID { return c.ID })
	return items, next, nil
}

// GetCredential returns one credential.
func (s *Service) GetCredential(ctx context.Context, orgID, credentialID uuid.UUID) (Credential, error) {
	if err := authz.RequireInOrg(ctx, PermRead, orgID); err != nil {
		return Credential{}, err
	}
	var c Credential
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) (err error) {
		if c, err = scanCredential(tx.QueryRow(ctx, `SELECT `+viewColumns+viewFrom+` WHERE c.org_id = $1 AND c.id = $2`, orgID, credentialID)); err != nil {
			return err
		}
		return siteScope(ctx, tx, PermRead, c.SiteID)
	})
	return c, dbProblem(err, "credential")
}

// RevokeCredential revokes a manual credential: it leaves the allow-list by
// delta and the site's agents get `revoke_credential` so it stops working at
// once. Derived credentials end with their vehicle or visitor entry and
// answer 409 here (permission access.manage).
func (s *Service) RevokeCredential(ctx context.Context, orgID, credentialID uuid.UUID) error {
	if err := authz.RequireAnywhere(ctx, PermManage); err != nil {
		return err
	}
	var siteID uuid.UUID
	var agents []uuid.UUID
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		c, err := scanCredential(tx.QueryRow(ctx, `SELECT `+viewColumns+viewFrom+` WHERE c.org_id = $1 AND c.id = $2 FOR UPDATE OF c`, orgID, credentialID))
		if err != nil {
			return err
		}
		siteID = c.SiteID
		if err := siteScope(ctx, tx, PermManage, c.SiteID); err != nil {
			return err
		}
		if c.SourceType != SourceManual {
			return problem.Conflict("a derived credential ends with its vehicle or visitor entry; it cannot be revoked here")
		}
		if c.Status == StatusRevoked {
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE credential SET status = 'revoked', status_reason = 'manual', updated_at = now() WHERE org_id = $1 AND id = $2`, orgID, credentialID); err != nil {
			return err
		}
		points, err := loadPoints(ctx, tx, orgID, c.SiteID)
		if err != nil {
			return err
		}
		if _, err := syncCredentialByID(ctx, tx, orgID, c.SiteID, credentialID, points); err != nil {
			return err
		}
		if agents, err = s.queueRevokeCommands(ctx, tx, orgID, c.SiteID, credentialID); err != nil {
			return err
		}
		diff, err := audit.Diff("credential", map[string]any{"status": c.Status}, map[string]any{"status": StatusRevoked})
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &c.SiteID, Action: "access.credential_revoke", TargetType: "credential", TargetID: credentialID, Diff: diff})
	})
	if err != nil {
		return dbProblem(err, "credential")
	}
	s.WakeSite(siteID)
	for _, a := range agents {
		s.hub.NotifyAgent(a)
	}
	return nil
}

// queueRevokeCommands queues revoke_credential for every active agent of the
// site (the removal also arrives in the next delta, so repeating is harmless).
func (s *Service) queueRevokeCommands(ctx context.Context, tx pgx.Tx, orgID, siteID, credentialID uuid.UUID) ([]uuid.UUID, error) {
	payload, err := json.Marshal(protocol.Command{Command: protocol.CmdRevokeCredential, CredentialID: credentialID.String()})
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id FROM agent WHERE org_id = $1 AND site_id = $2 AND status = 'active'`, orgID, siteID)
	if err != nil {
		return nil, err
	}
	var agents []uuid.UUID
	for rows.Next() {
		var a uuid.UUID
		if err := rows.Scan(&a); err != nil {
			rows.Close()
			return nil, err
		}
		agents = append(agents, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	now := s.now()
	for _, a := range agents {
		if _, err := tx.Exec(ctx, `INSERT INTO agent_command (id, org_id, site_id, agent_id, command, payload, issued_at, expires_at)
			VALUES ($1, $2, $3, $4, 'revoke_credential', $5, $6, $7)`, id.New(), orgID, siteID, a, payload, now, now.Add(10*time.Minute)); err != nil {
			return nil, err
		}
	}
	return agents, nil
}

// RequestSnapshot queues a request_snapshot command for an agent: it applies
// the next snapshot as a full replacement (permission access.manage).
func (s *Service) RequestSnapshot(ctx context.Context, orgID, agentID uuid.UUID) (uuid.UUID, error) {
	if err := authz.RequireAnywhere(ctx, PermManage); err != nil {
		return uuid.Nil, err
	}
	var cmdID uuid.UUID
	err := s.db.InTenantTx(ctx, orgID, func(tx pgx.Tx) error {
		a, err := s.loadAgent(ctx, tx, orgID, agentID, false)
		if err != nil {
			return err
		}
		if err := siteScope(ctx, tx, PermManage, a.SiteID); err != nil {
			return err
		}
		if a.Status != "active" {
			return problem.Conflict("the agent is not active")
		}
		payload, _ := json.Marshal(protocol.Command{Command: protocol.CmdRequestSnapshot})
		now := s.now()
		cmdID = id.New()
		if _, err := tx.Exec(ctx, `INSERT INTO agent_command (id, org_id, site_id, agent_id, command, payload, issued_at, expires_at)
			VALUES ($1, $2, $3, $4, 'request_snapshot', $5, $6, $7)`, cmdID, orgID, a.SiteID, agentID, payload, now, now.Add(10*time.Minute)); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: orgID, SiteID: &a.SiteID, Action: "access.snapshot_requested", TargetType: "agent", TargetID: agentID})
	})
	if err != nil {
		return uuid.Nil, dbProblem(err, "agent")
	}
	s.hub.NotifyAgent(agentID)
	return cmdID, nil
}
