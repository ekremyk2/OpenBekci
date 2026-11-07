import { queryOptions } from '@tanstack/react-query'
import { draftApi } from '@/api/draft'

export interface UnitFilters {
  siteId: string
  blockId?: string
  type?: string
  vacant?: boolean
  q?: string
  parentUnitId?: string
  tagId?: string
  cursor?: string
}

export const unitQueries = {
  list: (orgId: string, f: UnitFilters, limit = 25) =>
    queryOptions({
      queryKey: ['tenancy.listUnits', orgId, f, limit],
      queryFn: async () => {
        const { data } = await draftApi.GET('/v1/orgs/{org_id}/units', {
          params: {
            path: { org_id: orgId },
            query: {
              site_id: f.siteId,
              block_id: f.blockId,
              type: f.type,
              vacant: f.vacant,
              q: f.q,
              parent_unit_id: f.parentUnitId,
              tag_id: f.tagId,
              cursor: f.cursor,
              limit,
            },
          },
        })
        return data!
      },
    }),
  one: (orgId: string, unitId: string) =>
    queryOptions({
      queryKey: ['tenancy.getUnit', orgId, unitId],
      queryFn: async () => {
        const { data } = await draftApi.GET('/v1/orgs/{org_id}/units/{unit_id}', {
          params: { path: { org_id: orgId, unit_id: unitId } },
        })
        return data!
      },
    }),
  tags: (orgId: string, siteId: string) =>
    queryOptions({
      queryKey: ['tenancy.listUnitTags', orgId, siteId],
      queryFn: async () => {
        const { data } = await draftApi.GET('/v1/orgs/{org_id}/unit-tags', {
          params: { path: { org_id: orgId }, query: { site_id: siteId, limit: 100 } },
        })
        return data!
      },
      staleTime: 5 * 60_000,
    }),
  tagMembers: (orgId: string, tagId: string) =>
    queryOptions({
      queryKey: ['tenancy.getUnitTagMembers', orgId, tagId],
      queryFn: async () => {
        const { data } = await draftApi.GET('/v1/orgs/{org_id}/unit-tags/{tag_id}/units', {
          params: { path: { org_id: orgId, tag_id: tagId } },
        })
        return data!
      },
    }),
  vehicles: (orgId: string, siteId: string, unitId: string) =>
    queryOptions({
      queryKey: ['tenancy.listVehicles', orgId, unitId],
      queryFn: async () => {
        const { data } = await draftApi.GET('/v1/orgs/{org_id}/vehicles', {
          params: {
            path: { org_id: orgId },
            query: { site_id: siteId, unit_id: unitId, limit: 50 },
          },
        })
        return data!
      },
    }),
}

export const UNIT_TYPES = [
  'flat',
  'shop',
  'office',
  'parking',
  'storage',
  'common',
  'other',
] as const
