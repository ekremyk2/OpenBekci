import { http } from 'msw'
import { server } from '@/test/setup'
import { db } from '@/test/handlers/operations'

export const PREVIEW = '*/v1/orgs/:org/announcements\\:preview'

export function seedDraft(channels = ['in_app', 'sms']) {
  const at = new Date().toISOString()
  const a = {
    id: 'a-draft',
    org_id: 'org-1',
    site_id: 'site-a',
    title: 'Water cut',
    body_md: 'No water from 10:00',
    status: 'draft',
    audience: { kind: 'site' },
    publish_at: null,
    expires_at: null,
    pinned: false,
    channels,
    recipient_count: null,
    sms_count: null,
    read_count: 0,
    read_at: null,
    file_ids: [],
    published_at: null,
    created_by: 'u-manager',
    created_at: at,
    updated_at: at,
    version: 1,
  }
  db.announcements.push(a)
  return a
}

/** Records the headers and body of every call to a path, and passes the request on to the mock. */
export function spy(pathPattern: string, method: 'post' | 'patch' = 'post') {
  const calls: { key: string | null; body: unknown; url: string }[] = []
  server.use(
    http[method](pathPattern, async ({ request }) => {
      calls.push({
        key: request.headers.get('Idempotency-Key'),
        body: await request.clone().json(),
        url: request.url,
      })
      return undefined
    }),
  )
  return calls
}
