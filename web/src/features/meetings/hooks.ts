import { useMemo } from 'react'
import { gApi, unwrap, useOrgQuery, type S } from './kit/api'
import { ownerRows } from './logic'

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
      unwrap(gApi.GET('/v1/orgs/{org_id}/persons', { params: { path: { org_id: orgId }, query: { limit: 200 } } })).then(
        (r) => r.items,
      ),
    { staleTime: 5 * 60_000 },
  )
}

export function useUnits(orgId: string, siteId: string) {
  return useOrgQuery(
    ['units', siteId],
    () =>
      unwrap(
        gApi.GET('/v1/orgs/{org_id}/units', { params: { path: { org_id: orgId }, query: { site_id: siteId, limit: 200 } } }),
      ).then((r) => r.items),
    { staleTime: 5 * 60_000 },
  )
}

/** One row per owner with the units they hold (occupancies of kind owner). */
export function useOwnerRows(orgId: string, siteId: string) {
  const q = useOrgQuery(
    ['owners', siteId],
    () =>
      unwrap(
        gApi.GET('/v1/orgs/{org_id}/occupancies', {
          params: { path: { org_id: orgId }, query: { site_id: siteId, limit: 200 } },
        }),
      ).then((r) => r.items),
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

export function useDecisions(orgId: string, siteId: string, query: Record<string, string | number | undefined> = {}) {
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
