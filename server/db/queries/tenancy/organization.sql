-- name: GetOrganization :one
SELECT * FROM organization WHERE id = $1;

-- name: ListOrganizations :many
SELECT * FROM organization ORDER BY slug;
