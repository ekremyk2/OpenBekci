import { moneyIdempotentOperations } from './idempotent.money.gen'

/**
 * STUB. Operations tagged `x-idempotent` in api/openapi.yaml, as "METHOD /path/template" keys.
 * A build-time script will regenerate this file from the spec once it exists (web plan §6);
 * until then the map is empty. Tests inject their own set into the middleware.
 * Per-module lists (money so far) are spread in below.
 */
export const idempotentOperations: ReadonlySet<string> = new Set<string>([
  ...moneyIdempotentOperations,
])
