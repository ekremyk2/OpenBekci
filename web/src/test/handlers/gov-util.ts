import { HttpResponse } from 'msw'

/** Shared helpers of the in-memory governance, community and access handlers. */
export const ORG = 'org-1'
export const SITE = 'site-a'

let counter = 100
export const uid = (prefix: string) => `${prefix}-${++counter}`

export function resetCounter() {
  counter = 100
}

/** RFC 9457 problem answer in the shape the client's error mapping reads. */
export function problem(
  status: number,
  code: string,
  extra: { detail?: string; errors?: { field: string; code: string; message?: string }[] } = {},
) {
  return HttpResponse.json(
    { type: `https://openbekci.org/problems/${code}`, title: code, status, code, ...extra },
    { status, headers: { 'content-type': 'application/problem+json' } },
  )
}

export const list = <T>(items: T[]) => HttpResponse.json({ items, next_cursor: null })

export const nowIso = () => new Date().toISOString()

export const daysFromNow = (days: number, hour = 18) => {
  const d = new Date()
  d.setUTCDate(d.getUTCDate() + days)
  d.setUTCHours(hour, 0, 0, 0)
  return d.toISOString()
}

/** `If-Match: "n"` against the stored version; returns a 409 problem when it does not match. */
export function versionMismatch(request: Request, current: number) {
  const raw = request.headers.get('If-Match')?.replaceAll('"', '')
  if (raw === undefined || raw === null) {
    return problem(422, 'validation_failed', {
      errors: [{ field: 'If-Match', code: 'required' }],
    })
  }
  return Number(raw) === current ? null : problem(409, 'version_conflict')
}

export function qs(request: Request) {
  return new URL(request.url).searchParams
}

export const PDF_BYTES = new Uint8Array([
  0x25, 0x50, 0x44, 0x46, 0x2d, 0x31, 0x2e, 0x34, 0x0a, 0x25, 0x25, 0x45, 0x4f, 0x46,
])

export function pdf() {
  return new HttpResponse(PDF_BYTES, { headers: { 'content-type': 'application/pdf' } })
}

/** Directory of the mock site: six owner households. Land shares (arsa payi) sum to 100. */
export interface MockOwner {
  personId: string
  name: string
  unitId: string
  door: string
  share: number
  phone: string
}
export const OWNERS: MockOwner[] = [
  { personId: 'p-ayse', name: 'Ayşe Yılmaz', unitId: 'u-a1', door: 'A1', share: 20, phone: '+905551110001' },
  { personId: 'p-mehmet', name: 'Mehmet Demir', unitId: 'u-a2', door: 'A2', share: 20, phone: '+905551110002' },
  { personId: 'p-zeynep', name: 'Zeynep Kaya', unitId: 'u-b1', door: 'B1', share: 15, phone: '+905551110003' },
  { personId: 'p-can', name: 'Can Öztürk', unitId: 'u-b2', door: 'B2', share: 15, phone: '+905551110004' },
  { personId: 'p-elif', name: 'Elif Şahin', unitId: 'u-c1', door: 'C1', share: 15, phone: '+905551110005' },
  { personId: 'p-ali', name: 'Ali Arslan', unitId: 'u-c2', door: 'C2', share: 15, phone: '+905551110006' },
]
export const unitDoor = (unitId: string) => OWNERS.find((o) => o.unitId === unitId)?.door ?? unitId
