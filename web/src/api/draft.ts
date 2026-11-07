// Types of the DRAFT contract (clients/ts/draft.d.ts, generated from api/openapi-draft.yaml).
// Operations of releases 0.1 to 0.3 live here until they are promoted into the served spec; then
// this file is deleted and callers move to `@/api/schema`.
import createClient from 'openapi-fetch'
import type { components, paths } from '../../../clients/ts/draft'
import { createMiddleware, getLocaleProvider } from './client'
import { idempotentOperations } from './idempotent.gen'

export type { paths as DraftPaths, components as DraftComponents }
type S = components['schemas']

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
export type ApiFile = S['File']
export type AuditEvent = S['AuditEvent']
export type SiteDashboard = S['SiteDashboard']
export type PlatformOrg = S['PlatformOrg']
export type PlatformHealth = S['PlatformHealth']
export type BackupStatus = S['BackupStatus']
export type PlatformJob = S['PlatformJob']
export type PlatformSettings = S['PlatformSettings']
export type Job = S['Job']
export type Channel = 'in_app' | 'email' | 'sms' | 'push'

/** `params.header` for operations that require `If-Match` (the entity's `version`, quoted). */
export function ifMatch(version: number | string) {
  return { 'If-Match': `"${version}"` }
}

/** Typed client for the draft operations; same middleware (CSRF marker, locale, If-Match, errors). */
export function createDraftClient(baseUrl = globalThis.location?.origin ?? '') {
  const client = createClient<paths>({
    baseUrl,
    credentials: 'include',
    fetch: (request: Request) => globalThis.fetch(request),
  })
  client.use(
    createMiddleware({ getLocale: () => getLocaleProvider()(), idempotent: idempotentOperations }),
  )
  return client
}

export const apiDraft = createDraftClient()
