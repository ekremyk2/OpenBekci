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

type S = components['schemas']
// Short names used by the operations screens (tickets, announcements, documents, notifications,
// audit, dashboards, platform). Anything else: `Schema<'Name'>`.
export type Ticket = S['Ticket']
export type TicketStatus = S['TicketStatus']
export type TicketWorkflow = S['TicketWorkflow']
export type TicketCategory = S['TicketCategory']
export type TicketComment = S['TicketComment']
export type TicketAttachment = S['TicketAttachment']
export type TicketStatusChange = S['TicketStatusChange']
export type Announcement = S['Announcement']
export type AnnouncementAudience = S['AnnouncementAudience']
export type AnnouncementPreview = S['AnnouncementPreview']
export type AnnouncementRead = S['AnnouncementRead']
export type Folder = S['Folder']
export type Document = S['Document']
export type DocumentVisibility = S['DocumentVisibility']
export type Notification = S['Notification']
export type NotificationDefault = S['NotificationDefault']
export type AuditEvent = S['AuditEvent']
export type SiteDashboard = S['SiteDashboard']
export type PlatformOrg = S['PlatformOrg']
export type PlatformHealth = S['PlatformHealth']
export type BackupStatus = S['BackupStatus']
export type PlatformJob = S['PlatformJob']
export type PlatformSettings = S['PlatformSettings']
export type Job = S['Job']
export type ApiFile = S['File']
export type Channel = 'in_app' | 'email' | 'sms' | 'push' | 'chat'

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

/**
 * `If-Match` for an entity whose `version` is known. A missing version is a programming or
 * loading error, never silently widened to "any version" (that would drop lost-update protection).
 */
export function ifMatchOf(version: number | string | undefined) {
  if (version === undefined) throw new Error('Cannot save: the current version is not loaded yet')
  return ifMatch(version)
}

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
