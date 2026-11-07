-- The ONE definition of "a membership that is valid now": valid_from <= now < valid_to
-- (open-ended when valid_to is null), of a person linked to the user, in an active
-- organization. GET /v1/me and the authorization Resolver both read through it, so
-- what /v1/me shows and what is enforced cannot drift apart.
--
-- A person without a valid membership still yields one row (role is null): /v1/me
-- lists the organization; the Resolver ignores such rows.

-- name: ListUserMemberships :many
SELECT o.id AS org_id, o.slug, o.name, o.default_locale, p.display_name,
       m.role AS role, m.scope_type AS scope_type, m.scope_id AS scope_id, m.site_owned AS site_owned
FROM person p
JOIN organization o ON o.id = p.org_id AND o.status = 'active'
LEFT JOIN membership m ON m.org_id = p.org_id AND m.person_id = p.id
     AND m.valid_from <= @now::timestamptz AND (m.valid_to IS NULL OR m.valid_to > @now::timestamptz)
WHERE p.user_id = @user_id::uuid
  AND (sqlc.narg(org_id)::uuid IS NULL OR p.org_id = sqlc.narg(org_id)::uuid)
ORDER BY o.name, o.id, p.created_at, m.role, m.scope_type, m.scope_id NULLS FIRST;
