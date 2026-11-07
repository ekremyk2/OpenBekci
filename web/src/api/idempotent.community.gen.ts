/**
 * `x-idempotent: required` operations of the governance (0.2), community and access (0.3) draft
 * modules, as "METHOD /path/template" keys (api/openapi-draft.yaml). The optional ones accept a key
 * but do not demand one; the screens send a key for those that create things anyway.
 */
export const communityIdempotentOperations: readonly string[] = [
  'POST /v1/orgs/{org_id}/meetings/{meeting_id}:notify-decisions',
  'POST /v1/orgs/{org_id}/decisions/{decision_id}:apply',
  'POST /v1/orgs/{org_id}/bookings',
  'POST /v1/orgs/{org_id}/bookings/{booking_id}:cancel',
  'POST /v1/orgs/{org_id}/credentials',
  'POST /v1/orgs/{org_id}/access-points/{point_id}:open',
]
