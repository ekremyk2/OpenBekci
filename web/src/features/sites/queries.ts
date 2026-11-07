import { queryOptions } from '@tanstack/react-query'
import { draftApi } from '@/api/draft'

/** Query options for the tenancy module (web plan §6: keys are [operation, params]). */
export const siteQueries = {
  list: (orgId: string, params: { status?: string; q?: string; cursor?: string } = {}) =>
    queryOptions({
      queryKey: ['tenancy.listSites', orgId, params],
      queryFn: async () => {
        const { data } = await draftApi.GET('/v1/orgs/{org_id}/sites', {
          params: { path: { org_id: orgId }, query: { ...params, limit: 25 } },
        })
        return data!
      },
    }),
  one: (orgId: string, siteId: string) =>
    queryOptions({
      queryKey: ['tenancy.getSite', orgId, siteId],
      queryFn: async () => {
        const { data } = await draftApi.GET('/v1/orgs/{org_id}/sites/{site_id}', {
          params: { path: { org_id: orgId, site_id: siteId } },
        })
        return data!
      },
    }),
  settings: (orgId: string, siteId: string) =>
    queryOptions({
      queryKey: ['tenancy.getSiteSettings', orgId, siteId],
      queryFn: async () => {
        const { data } = await draftApi.GET('/v1/orgs/{org_id}/sites/{site_id}/settings', {
          params: { path: { org_id: orgId, site_id: siteId } },
        })
        return data!
      },
    }),
  bank: (orgId: string, siteId: string) =>
    queryOptions({
      queryKey: ['tenancy.getBankDetails', orgId, siteId],
      retry: false,
      queryFn: async () => {
        const { data } = await draftApi.GET('/v1/orgs/{org_id}/sites/{site_id}/bank-details', {
          params: { path: { org_id: orgId, site_id: siteId }, query: {} },
        })
        return data!
      },
    }),
  lock: (orgId: string, siteId: string) =>
    queryOptions({
      queryKey: ['money.getLockDate', orgId, siteId],
      queryFn: async () => {
        const { data } = await draftApi.GET('/v1/orgs/{org_id}/sites/{site_id}/lock-date', {
          params: { path: { org_id: orgId, site_id: siteId } },
        })
        return data!
      },
    }),
  blocks: (orgId: string, siteId: string) =>
    queryOptions({
      queryKey: ['tenancy.listBlocks', orgId, siteId],
      queryFn: async () => {
        const { data } = await draftApi.GET('/v1/orgs/{org_id}/blocks', {
          params: { path: { org_id: orgId }, query: { site_id: siteId, limit: 200 } },
        })
        return data!
      },
      staleTime: 5 * 60_000,
    }),
  transfers: (orgId: string) =>
    queryOptions({
      queryKey: ['tenancy.listSiteTransfers', orgId],
      queryFn: async () => {
        const { data } = await draftApi.GET('/v1/orgs/{org_id}/site-transfers', {
          params: { path: { org_id: orgId }, query: { limit: 10 } },
        })
        return data!
      },
    }),
}
