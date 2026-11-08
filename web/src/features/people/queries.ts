import { queryOptions } from '@tanstack/react-query'
import { draftApi } from '@/api/draft'

export const personQueries = {
  list: (
    orgId: string,
    p: { siteId?: string; q?: string; joinStatus?: string; unitId?: string; cursor?: string },
  ) =>
    queryOptions({
      queryKey: ['people.listPersons', orgId, p],
      queryFn: async () => {
        const { data } = await draftApi.GET('/v1/orgs/{org_id}/persons', {
          params: {
            path: { org_id: orgId },
            query: {
              site_id: p.siteId,
              q: p.q,
              join_status: p.joinStatus,
              unit_id: p.unitId,
              cursor: p.cursor,
              limit: 25,
            },
          },
        })
        return data!
      },
    }),
  one: (orgId: string, personId: string) =>
    queryOptions({
      queryKey: ['people.getPerson', orgId, personId],
      queryFn: async () => {
        const { data } = await draftApi.GET('/v1/orgs/{org_id}/persons/{person_id}', {
          params: { path: { org_id: orgId, person_id: personId } },
        })
        return data!
      },
    }),
  occupancies: (orgId: string, personId: string) =>
    queryOptions({
      queryKey: ['people.listOccupancies', orgId, { person_id: personId }],
      queryFn: async () => {
        const { data } = await draftApi.GET('/v1/orgs/{org_id}/occupancies', {
          params: { path: { org_id: orgId }, query: { person_id: personId, limit: 100 } },
        })
        return data!
      },
    }),
  occupancy: (orgId: string, occupancyId: string) =>
    queryOptions({
      queryKey: ['people.getOccupancy', orgId, occupancyId],
      queryFn: async () => {
        const { data } = await draftApi.GET('/v1/orgs/{org_id}/occupancies/{occupancy_id}', {
          params: { path: { org_id: orgId, occupancy_id: occupancyId } },
        })
        return data!
      },
    }),
  memberships: (orgId: string, personId: string) =>
    queryOptions({
      queryKey: ['people.listMemberships', orgId, personId],
      queryFn: async () => {
        const { data } = await draftApi.GET('/v1/orgs/{org_id}/memberships', {
          params: { path: { org_id: orgId }, query: { person_id: personId, limit: 100 } },
        })
        return data!
      },
    }),
  invitations: (orgId: string, p: { status?: string; personId?: string; cursor?: string }) =>
    queryOptions({
      queryKey: ['people.listInvitations', orgId, p],
      queryFn: async () => {
        const { data } = await draftApi.GET('/v1/orgs/{org_id}/invitations', {
          params: {
            path: { org_id: orgId },
            query: { status: p.status, person_id: p.personId, cursor: p.cursor, limit: 25 },
          },
        })
        return data!
      },
    }),
  claims: (orgId: string, p: { status?: string; cursor?: string }) =>
    queryOptions({
      queryKey: ['people.listUnitClaims', orgId, p],
      queryFn: async () => {
        const { data } = await draftApi.GET('/v1/orgs/{org_id}/unit-claims', {
          params: {
            path: { org_id: orgId },
            query: { status: p.status, cursor: p.cursor, limit: 25 },
          },
        })
        return data!
      },
    }),
  directory: (orgId: string, p: { siteId: string; q?: string; cursor?: string }) =>
    queryOptions({
      queryKey: ['people.listDirectory', orgId, p],
      queryFn: async () => {
        const { data } = await draftApi.GET('/v1/orgs/{org_id}/directory', {
          params: {
            path: { org_id: orgId },
            query: { site_id: p.siteId, q: p.q, cursor: p.cursor, limit: 50 },
          },
        })
        return data!
      },
    }),
  vendors: (orgId: string, p: { q?: string; archived?: boolean; cursor?: string }) =>
    queryOptions({
      queryKey: ['people.listVendors', orgId, p],
      queryFn: async () => {
        const { data } = await draftApi.GET('/v1/orgs/{org_id}/vendors', {
          params: {
            path: { org_id: orgId },
            query: { q: p.q, archived: p.archived, cursor: p.cursor, limit: 25 },
          },
        })
        return data!
      },
    }),
}
