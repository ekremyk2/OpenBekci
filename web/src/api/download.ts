import type { Client } from 'openapi-fetch'
import { api } from './client'
import type { paths } from './draft'

/**
 * File downloads (PDF receipts and statements, CSV/XLSX reports) go through the SAME typed client
 * as every other call, so a spec change fails typecheck here, the middleware adds the CSRF marker,
 * and a 401 or a `reauth_required` on a download emits the usual API event (sign-out redirect,
 * re-authentication). The report endpoints choose the file type with the contract's `format`
 * query parameter.
 */
const client = api as unknown as Client<paths>

/** Every GET path of the draft contract that serves a file. */
export type DownloadEndpoint =
  | '/v1/orgs/{org_id}/payments/{payment_id}/receipt'
  | '/v1/orgs/{org_id}/files/{file_id}'
  | '/v1/orgs/{org_id}/meetings/{meeting_id}/call-letter'
  | '/v1/orgs/{org_id}/meetings/{meeting_id}/attendance-sheet'
  | '/v1/orgs/{org_id}/debt-breakdown'
  | '/v1/orgs/{org_id}/units/{unit_id}/statements/{debtor_kind}'
  | '/v1/orgs/{org_id}/persons/{person_id}/statement'
  | '/v1/orgs/{org_id}/reports/income-expense'
  | '/v1/orgs/{org_id}/reports/debtors'
  | '/v1/orgs/{org_id}/reports/budget-vs-actual'
  | '/v1/orgs/{org_id}/reports/trial-balance'
  | '/v1/orgs/{org_id}/reports/receivables-aging'
  | '/v1/orgs/{org_id}/reports/collector-reconciliation'
  | '/v1/orgs/{org_id}/reports/bank-reconciliation'
  | '/v1/orgs/{org_id}/reports/fund-balances'

// compile-time proof that every endpoint above is a real GET path of the contract
type Checked<T extends keyof paths> = T
export type _EveryEndpointExists = Checked<DownloadEndpoint>

export type DownloadFormat = 'csv' | 'xlsx' | 'pdf'

export interface DownloadRequest {
  endpoint: DownloadEndpoint
  path: Record<string, string>
  query?: Record<string, string | undefined>
  format?: DownloadFormat
}

export async function downloadFile(req: DownloadRequest, filename: string): Promise<void> {
  const query = { ...req.query, ...(req.format ? { format: req.format } : {}) }
  const { data } = await client.GET(
    req.endpoint as never,
    {
      params: { path: req.path, query },
      parseAs: 'blob',
    } as never,
  )
  const blob = data as unknown as Blob
  const href = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = href
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(href), 10_000)
}
