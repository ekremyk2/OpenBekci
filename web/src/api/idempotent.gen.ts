import { communityIdempotentOperations } from './idempotent.community.gen'

/**
 * STUB. Operations tagged `x-idempotent` in api/openapi.yaml, as "METHOD /path/template" keys.
 * A build-time script will regenerate this file from the spec once it exists (web plan §6);
 * until then the map is empty. Tests inject their own set into the middleware.
 */
export const idempotentOperations: ReadonlySet<string> = new Set<string>([
  // 'POST /v1/orgs/{org_id}/payments',
  ...communityIdempotentOperations,
])
