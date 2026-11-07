/**
 * Money operations tagged x-idempotent: required in the DRAFT contract (api/DRAFT-INDEX.md). Merged into
 * idempotent.gen.ts; regenerated together with it once the spec is served.
 */
export const moneyIdempotentOperations: readonly string[] = [
  'POST /v1/orgs/{org_id}/journal-entries/{entry_id}:reverse',
  'POST /v1/orgs/{org_id}/opening-balances',
  'POST /v1/orgs/{org_id}/opening-balances:import',
  'POST /v1/orgs/{org_id}/expense-allocations',
  'POST /v1/orgs/{org_id}/charges',
  'POST /v1/orgs/{org_id}/charges/{charge_id}:cancel',
  'POST /v1/orgs/{org_id}/charges/{charge_id}:adjust',
  'POST /v1/orgs/{org_id}/late-fee-waivers',
  'POST /v1/orgs/{org_id}/payments',
  'POST /v1/orgs/{org_id}/payments/{payment_id}:void',
  'PUT /v1/orgs/{org_id}/payments/{payment_id}/allocations',
  'POST /v1/orgs/{org_id}/payments:declare',
  'POST /v1/orgs/{org_id}/payments/{payment_id}:confirm',
  'POST /v1/orgs/{org_id}/credits/{credit_id}:apply',
  'POST /v1/orgs/{org_id}/transfers',
  'POST /v1/orgs/{org_id}/refunds',
  'POST /v1/orgs/{org_id}/bank-transactions/{transaction_id}:match',
  'POST /v1/orgs/{org_id}/bank-transactions/{transaction_id}:unmatch',
  'POST /v1/orgs/{org_id}/expenses',
  'POST /v1/orgs/{org_id}/expenses/{expense_id}:void',
  'POST /v1/orgs/{org_id}/recurring-expenses',
  'POST /v1/orgs/{org_id}/bills',
  'POST /v1/orgs/{org_id}/bills/{bill_id}:cancel',
  'POST /v1/orgs/{org_id}/bills/{bill_id}/payments',
]
