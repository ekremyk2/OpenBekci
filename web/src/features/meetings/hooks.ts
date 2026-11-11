import { useMemo } from 'react'
import { useSession } from '@/app/session'
import { gApi, unwrap, useOrgQuery, type S } from './kit/api'
import { ownerRows } from './logic'

/** Every page of a cursor list (the served page size maximum is 200). */
async function allPages<T>(
  page: (cursor: string | undefined) => Promise<{ items: T[]; next_cursor: string | null }>,
): Promise<T[]> {
  const out: T[] = []
  let cursor: string | undefined
  do {
    const r = await page(cursor)
    out.push(...r.items)
    cursor = r.next_cursor ?? undefined
  } while (cursor)
  return out
}

export const GOV_SCALE = 'meeting_notice_proof'

export function useMeeting(orgId: string, meetingId: string, refetchMs?: number | false) {
  return useOrgQuery(
    ['meeting', meetingId],
    () =>
      unwrap(
        gApi.GET('/v1/orgs/{org_id}/meetings/{meeting_id}', {
          params: { path: { org_id: orgId, meeting_id: meetingId } },
        }),
      ),
    { refetchInterval: refetchMs ?? false },
  )
}

export function useGovernanceSettings(orgId: string, siteId: string) {
  return useOrgQuery(['gov-settings', siteId], () =>
    unwrap(
      gApi.GET('/v1/orgs/{org_id}/sites/{site_id}/governance-settings', {
        params: { path: { org_id: orgId, site_id: siteId } },
      }),
    ),
  )
}

export function usePersons(orgId: string) {
  return useOrgQuery(
    ['persons'],
    () =>
      allPages((cursor) =>
        unwrap(
          gApi.GET('/v1/orgs/{org_id}/persons', {
            params: { path: { org_id: orgId }, query: { limit: 200, cursor } },
          }),
        ),
      ),
    { staleTime: 5 * 60_000 },
  )
}

type UnitLite = Pick<S['Unit'], 'id' | 'door_number'>

/**
 * Units of the site. Without `units.read` (residents) these are the user's own units from the
 * session, so no forbidden call is made.
 */
export function useUnits(orgId: string, siteId: string) {
  const { can, me } = useSession()
  const canList = can('units.read')
  const q = useOrgQuery(
    ['units', siteId],
    () =>
      allPages((cursor) =>
        unwrap(
          gApi.GET('/v1/orgs/{org_id}/units', {
            params: { path: { org_id: orgId }, query: { site_id: siteId, limit: 200, cursor } },
          }),
        ),
      ),
    { staleTime: 5 * 60_000, enabled: canList },
  )
  const own: UnitLite[] = (me?.occupancies ?? [])
    .filter((o) => o.site_id === siteId)
    .map((o) => ({ id: o.unit_id, door_number: o.unit_label }))
  return canList
    ? (q as typeof q & { data: UnitLite[] | undefined })
    : { ...q, isError: false, data: own }
}

/**
 * One row per owner with the units they hold.
 * shortcut: occupancies carry no site id, so a multi-site org lists all its owners.
 */
export function useOwnerRows(orgId: string, siteId: string) {
  const q = useOrgQuery(
    ['owners', siteId],
    () =>
      allPages((cursor) =>
        unwrap(
          gApi.GET('/v1/orgs/{org_id}/occupancies', {
            params: {
              path: { org_id: orgId },
              query: { kind: 'owner', active: true, limit: 200, cursor },
            },
          }),
        ),
      ),
    { staleTime: 60_000 },
  )
  const rows = useMemo(() => (q.data ? ownerRows(q.data) : []), [q.data])
  return { ...q, rows }
}

export function useAgenda(orgId: string, meetingId: string) {
  return useOrgQuery(['agenda', meetingId], () =>
    unwrap(
      gApi.GET('/v1/orgs/{org_id}/meetings/{meeting_id}/agenda-items', {
        params: { path: { org_id: orgId, meeting_id: meetingId } },
      }),
    ).then((r) => r.items.sort((a, b) => a.position - b.position)),
  )
}

export function useNotices(orgId: string, meetingId: string, phase: 'call' | 'decisions') {
  return useOrgQuery(['notices', meetingId, phase], () =>
    unwrap(
      gApi.GET('/v1/orgs/{org_id}/meetings/{meeting_id}/notices', {
        params: { path: { org_id: orgId, meeting_id: meetingId }, query: { phase, limit: 200 } },
      }),
    ).then((r) => r.items),
  )
}

export function useDecisions(
  orgId: string,
  siteId: string,
  query: Record<string, string | number | undefined> = {},
) {
  return useOrgQuery(['decisions', siteId, query], () =>
    unwrap(
      gApi.GET('/v1/orgs/{org_id}/decisions', {
        params: { path: { org_id: orgId }, query: { site_id: siteId, limit: 200, ...query } },
      }),
    ).then((r) => r.items),
  )
}

export const THRESHOLD_RULE_LABEL_KEYS = [
  'simple_majority_present',
  'majority_count_and_share',
  'four_fifths',
  'unanimity',
] as const

export type Agenda = S['AgendaItem']
