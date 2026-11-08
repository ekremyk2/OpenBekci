/**
 * Types and client for the DRAFT contract (releases 0.1 to 0.3, `clients/ts/draft.d.ts`). The
 * backend for these endpoints does not exist yet; screens are built against MSW handlers written
 * from the spec examples. When the served spec (`schema.ts`) grows to cover them, point `api` at
 * the served types and delete this file.
 */
import { createApiClient } from './client'
import type { components, paths } from '../../../clients/ts/draft'

export type { components, paths }
export type DraftSchemas = components['schemas']

/** The draft contract's schema `Name`, for example `Schema<'Unit'>`. */
export type Schema<K extends keyof components['schemas']> = components['schemas'][K]

/** Client typed with the draft paths (same middleware as `api`). */
export const draftApi = createApiClient<paths>()

/**
 * The draft contract lists `If-Match` as a required header parameter on versioned operations, so
 * the generated types want it in `params.header`. Value: the entity's `version`, quoted (`"3"`).
 * `'*'` is used where the resource has no version field (see the PR notes on settings and bank
 * details).
 */
export const ifMatch = (version: number | string) => ({
  'If-Match': version === '*' ? '*' : `"${version}"`,
})

const isBlob = (v: unknown): boolean =>
  typeof v === 'object' && v !== null && typeof (v as Blob).arrayBuffer === 'function'

/**
 * `multipart/form-data` for upload endpoints (imports, archives). Spread into the request options
 * next to a placeholder `body`: `{ body: {} as never, ...multipart({ site_id, file, mapping }) }`.
 * Objects (the column mapping) travel as JSON text parts.
 */
export function multipart(fields: Record<string, string | Blob | object | null | undefined>) {
  return {
    bodySerializer: () => {
      const form = new FormData()
      for (const [key, value] of Object.entries(fields)) {
        if (value === undefined || value === null) continue
        if (isBlob(value)) form.append(key, value as Blob)
        else if (typeof value === 'object') form.append(key, JSON.stringify(value))
        else form.append(key, value)
      }
      return form
    },
  }
}
