import { queryOptions, useQuery } from '@tanstack/react-query'
import { apiDraft } from '@/api/draft'
import { must } from '@/lib/pagination'

const FIVE_MIN = 5 * 60_000

export const workflowQuery = (orgId: string, siteId: string) =>
  queryOptions({
    queryKey: ['operations.getTicketWorkflow', orgId, siteId],
    staleTime: FIVE_MIN,
    queryFn: async () =>
      must(
        await apiDraft.GET('/v1/orgs/{org_id}/ticket-workflow', {
          params: { path: { org_id: orgId }, query: { site_id: siteId } },
        }),
      ),
  })

export const categoriesQuery = (orgId: string, siteId: string, archived = false) =>
  queryOptions({
    queryKey: ['operations.listTicketCategories', orgId, siteId, archived],
    staleTime: FIVE_MIN,
    queryFn: async () =>
      must(
        await apiDraft.GET('/v1/orgs/{org_id}/ticket-categories', {
          params: {
            path: { org_id: orgId },
            query: { site_id: siteId, archived, limit: 100 },
          },
        }),
      ).items,
  })

export const ticketQuery = (orgId: string, ticketId: string) =>
  queryOptions({
    queryKey: ['operations.getTicket', orgId, ticketId],
    queryFn: async () =>
      must(
        await apiDraft.GET('/v1/orgs/{org_id}/tickets/{ticket_id}', {
          params: { path: { org_id: orgId, ticket_id: ticketId } },
        }),
      ),
  })

export const commentsQuery = (orgId: string, ticketId: string) =>
  queryOptions({
    queryKey: ['operations.listTicketComments', orgId, ticketId],
    queryFn: async () =>
      must(
        await apiDraft.GET('/v1/orgs/{org_id}/tickets/{ticket_id}/comments', {
          params: { path: { org_id: orgId, ticket_id: ticketId }, query: { limit: 100 } },
        }),
      ).items,
  })

export const historyQuery = (orgId: string, ticketId: string) =>
  queryOptions({
    queryKey: ['operations.listTicketHistory', orgId, ticketId],
    queryFn: async () =>
      must(
        await apiDraft.GET('/v1/orgs/{org_id}/tickets/{ticket_id}/history', {
          params: { path: { org_id: orgId, ticket_id: ticketId }, query: { limit: 100 } },
        }),
      ).items,
  })

export function useWorkflow(orgId: string, siteId: string | undefined) {
  return useQuery({ ...workflowQuery(orgId, siteId ?? ''), enabled: !!siteId })
}

export function useCategories(orgId: string, siteId: string | undefined, archived = false) {
  return useQuery({ ...categoriesQuery(orgId, siteId ?? '', archived), enabled: !!siteId })
}

/** Tickets are invalidated as a family after any ticket mutation. */
export const TICKET_KEYS = [
  'operations.listTickets',
  'operations.getTicket',
  'operations.listTicketComments',
  'operations.listTicketHistory',
] as const
