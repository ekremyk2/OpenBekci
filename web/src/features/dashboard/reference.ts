import { useQuery } from '@tanstack/react-query'
import { apiDraft } from '@/api/draft'
import { must } from '@/lib/pagination'

/*
 * Reference data shared by the operations screens (units, blocks, staff, vendors). Cached for
 * 5 minutes (web plan §6). Callers pass `enabled` when the caller may not read the list (a
 * resident picks from /v1/me occupancies instead).
 */
const FIVE_MIN = 5 * 60_000

export function useUnits(orgId: string, siteId: string | undefined, enabled = true) {
  return useQuery({
    queryKey: ['tenancy.listUnits', orgId, siteId],
    enabled: enabled && !!siteId,
    staleTime: FIVE_MIN,
    queryFn: async () =>
      must(
        await apiDraft.GET('/v1/orgs/{org_id}/units', {
          params: { path: { org_id: orgId }, query: { site_id: siteId!, limit: 200 } },
        }),
      ).items,
  })
}

export function useBlocks(orgId: string, siteId: string | undefined, enabled = true) {
  return useQuery({
    queryKey: ['tenancy.listBlocks', orgId, siteId],
    enabled: enabled && !!siteId,
    staleTime: FIVE_MIN,
    queryFn: async () =>
      must(
        await apiDraft.GET('/v1/orgs/{org_id}/blocks', {
          params: { path: { org_id: orgId }, query: { site_id: siteId!, limit: 100 } },
        }),
      ).items,
  })
}

/** People holding an active staff membership: the assignable staff of the organization. */
export function useStaff(orgId: string, enabled = true) {
  return useQuery({
    queryKey: ['people.listMemberships', orgId, 'staff'],
    enabled,
    staleTime: FIVE_MIN,
    queryFn: async () =>
      must(
        await apiDraft.GET('/v1/orgs/{org_id}/memberships', {
          params: { path: { org_id: orgId }, query: { role: 'staff', active: true, limit: 100 } },
        }),
      ).items,
  })
}

export function useVendors(orgId: string, enabled = true) {
  return useQuery({
    queryKey: ['people.listVendors', orgId],
    enabled,
    staleTime: FIVE_MIN,
    queryFn: async () =>
      must(
        await apiDraft.GET('/v1/orgs/{org_id}/vendors', {
          params: { path: { org_id: orgId }, query: { limit: 100 } },
        }),
      ).items,
  })
}
