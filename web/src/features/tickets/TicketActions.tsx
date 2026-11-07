import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Trans, useLingui } from '@lingui/react/macro'
import { GitMergeIcon, ThumbsUpIcon } from 'lucide-react'
import { apiDraft, ifMatch, type Ticket, type TicketWorkflow } from '@/api/draft'
import { AppError } from '@/api/errors'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import { Select } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { useStaff, useVendors } from '@/features/dashboard/reference'
import { useIdempotencyKey, withIdempotencyKey } from '@/lib/idempotency'
import { must } from '@/lib/pagination'
import { toast } from '@/lib/toast'
import { TICKET_KEYS } from './queries'

export function useInvalidateTickets() {
  const qc = useQueryClient()
  return () => Promise.all(TICKET_KEYS.map((k) => qc.invalidateQueries({ queryKey: [k] })))
}

/** "+1" on a common ticket (O3). Optimistic: it is a trivial toggle (web plan §6). */
export function VoteButton({ orgId, ticket }: { orgId: string; ticket: Ticket }) {
  const { t } = useLingui()
  const qc = useQueryClient()
  const key = ['operations.getTicket', orgId, ticket.id]
  const mutation = useMutation({
    mutationFn: async (vote: boolean) => {
      const params = { path: { org_id: orgId, ticket_id: ticket.id } }
      return must(
        vote
          ? await apiDraft.PUT('/v1/orgs/{org_id}/tickets/{ticket_id}/vote', { params })
          : await apiDraft.DELETE('/v1/orgs/{org_id}/tickets/{ticket_id}/vote', { params }),
      )
    },
    onMutate: async (vote) => {
      await qc.cancelQueries({ queryKey: key })
      const previous = qc.getQueryData<Ticket>(key)
      if (previous) {
        qc.setQueryData<Ticket>(key, {
          ...previous,
          voted_by_me: vote,
          vote_count: previous.vote_count + (vote ? 1 : -1),
        })
      }
      return { previous }
    },
    onError: (_e, _v, ctx) => {
      if (ctx?.previous) qc.setQueryData(key, ctx.previous)
      toast.error(t`Could not save your +1. Please try again.`)
    },
    onSettled: () => void qc.invalidateQueries({ queryKey: key }),
  })
  return (
    <Button
      type="button"
      variant={ticket.voted_by_me ? 'default' : 'outline'}
      aria-pressed={ticket.voted_by_me}
      onClick={() => mutation.mutate(!ticket.voted_by_me)}
    >
      <ThumbsUpIcon aria-hidden="true" />
      <Trans>Me too</Trans>
      <span aria-label={t`${ticket.vote_count} people confirmed this`}>+{ticket.vote_count}</span>
    </Button>
  )
}

/** Status change respecting the site workflow; the comment is optional and may stay internal. */
export function StatusChanger({
  orgId,
  ticket,
  workflow,
  canInternal,
}: {
  orgId: string
  ticket: Ticket
  workflow: TicketWorkflow
  canInternal: boolean
}) {
  const { t } = useLingui()
  const invalidate = useInvalidateTickets()
  const idem = useIdempotencyKey()
  const [statusId, setStatusId] = useState('')
  const [comment, setComment] = useState('')
  const [visibility, setVisibility] = useState<'public' | 'internal'>('public')
  const mutation = useMutation({
    mutationFn: async () =>
      must(
        await apiDraft.POST('/v1/orgs/{org_id}/tickets/{ticket_id}:transition', {
          params: { path: { org_id: orgId, ticket_id: ticket.id } },
          body: {
            status_id: statusId,
            comment: comment.trim() || undefined,
            comment_visibility: comment.trim() ? visibility : undefined,
          },
          ...withIdempotencyKey(idem.key),
        }),
      ),
    onSuccess: async () => {
      setStatusId('')
      setComment('')
      idem.renew()
      await invalidate()
      toast.info(t`Status updated.`)
    },
    onError: () => toast.error(t`Could not change the status. Please try again.`),
  })
  const options = workflow.statuses.filter((s) => s.id !== ticket.status_id)
  return (
    <form
      className="flex flex-col gap-2"
      onSubmit={(e) => {
        e.preventDefault()
        if (statusId) mutation.mutate()
      }}
    >
      <Label htmlFor="ticket-status">
        <Trans>Change status</Trans>
      </Label>
      <Select id="ticket-status" value={statusId} onChange={(e) => setStatusId(e.target.value)}>
        <option value="">{t`Choose a status`}</option>
        {options.map((s) => (
          <option key={s.id} value={s.id}>
            {s.name}
          </option>
        ))}
      </Select>
      <Textarea
        aria-label={t`Note with the status change`}
        placeholder={t`Add a note (optional)`}
        value={comment}
        onChange={(e) => setComment(e.target.value)}
        rows={2}
      />
      {canInternal && comment.trim() ? (
        <Select
          aria-label={t`Note visibility`}
          value={visibility}
          onChange={(e) => setVisibility(e.target.value as 'public' | 'internal')}
        >
          <option value="public">{t`Visible to the reporter`}</option>
          <option value="internal">{t`Internal note`}</option>
        </Select>
      ) : null}
      <Button type="submit" disabled={!statusId || mutation.isPending}>
        <Trans>Update status</Trans>
      </Button>
    </form>
  )
}

/** Assignment to a staff person or a vendor label (O2); manager only. */
export function AssigneeEditor({ orgId, ticket }: { orgId: string; ticket: Ticket }) {
  const { t } = useLingui()
  const invalidate = useInvalidateTickets()
  const staff = useStaff(orgId)
  const vendors = useVendors(orgId)
  const current = ticket.assignee
    ? ticket.assignee.kind === 'person'
      ? `p:${ticket.assignee.person_id}`
      : `v:${ticket.assignee.vendor_id}`
    : ''
  const mutation = useMutation({
    mutationFn: async (value: string) =>
      must(
        await apiDraft.PATCH('/v1/orgs/{org_id}/tickets/{ticket_id}', {
          params: {
            path: { org_id: orgId, ticket_id: ticket.id },
            header: ifMatch(ticket.version),
          },
          body: {
            assignee_person_id: value.startsWith('p:') ? value.slice(2) : null,
            assignee_vendor_id: value.startsWith('v:') ? value.slice(2) : null,
          },
        }),
      ),
    onSuccess: () => invalidate(),
    onError: (e) => {
      if (!(e instanceof AppError && e.isVersionConflict)) {
        toast.error(t`Could not assign the ticket. Please try again.`)
      }
    },
  })
  return (
    <div className="flex flex-col gap-2">
      <Label htmlFor="ticket-assignee">
        <Trans>Assigned to</Trans>
      </Label>
      <Select
        id="ticket-assignee"
        value={current}
        disabled={mutation.isPending}
        onChange={(e) => mutation.mutate(e.target.value)}
      >
        <option value="">{t`Unassigned`}</option>
        <optgroup label={t`Staff`}>
          {staff.data?.map((m) => (
            <option key={m.person_id} value={`p:${m.person_id}`}>
              {m.person_display_name}
            </option>
          ))}
        </optgroup>
        <optgroup label={t`Vendors`}>
          {vendors.data?.map((v) => (
            <option key={v.id} value={`v:${v.id}`}>
              {v.name}
            </option>
          ))}
        </optgroup>
      </Select>
    </div>
  )
}

/** Visibility override (O3): the category default can be changed per ticket by the manager. */
export function VisibilityEditor({ orgId, ticket }: { orgId: string; ticket: Ticket }) {
  const { t } = useLingui()
  const invalidate = useInvalidateTickets()
  const mutation = useMutation({
    mutationFn: async (visibility: 'private' | 'common') =>
      must(
        await apiDraft.PATCH('/v1/orgs/{org_id}/tickets/{ticket_id}', {
          params: {
            path: { org_id: orgId, ticket_id: ticket.id },
            header: ifMatch(ticket.version),
          },
          body: { visibility },
        }),
      ),
    onSuccess: () => invalidate(),
    onError: () => toast.error(t`Could not change the visibility. Please try again.`),
  })
  return (
    <div className="flex flex-col gap-2">
      <Label htmlFor="ticket-visibility">
        <Trans>Visibility</Trans>
      </Label>
      <Select
        id="ticket-visibility"
        value={ticket.visibility}
        disabled={mutation.isPending}
        onChange={(e) => mutation.mutate(e.target.value as 'private' | 'common')}
      >
        <option value="private">{t`Private: that unit and management`}</option>
        <option value="common">{t`Common: the whole site`}</option>
      </Select>
    </div>
  )
}

/** Merge duplicates (manager): votes carry over to the surviving ticket. */
export function MergeDialog({
  orgId,
  siteId,
  ticket,
}: {
  orgId: string
  siteId: string
  ticket: Ticket
}) {
  const { t } = useLingui()
  const [open, setOpen] = useState(false)
  const [target, setTarget] = useState('')
  const invalidate = useInvalidateTickets()
  const idem = useIdempotencyKey()
  const candidates = useQuery({
    queryKey: ['operations.listTickets', orgId, siteId, 'merge-candidates'],
    enabled: open,
    queryFn: async () =>
      must(
        await apiDraft.GET('/v1/orgs/{org_id}/tickets', {
          params: {
            path: { org_id: orgId },
            query: { site_id: siteId, semantic: 'open_like', limit: 100 },
          },
        }),
      ).items,
  })
  const mutation = useMutation({
    mutationFn: async () =>
      must(
        await apiDraft.POST('/v1/orgs/{org_id}/tickets/{ticket_id}:merge', {
          params: { path: { org_id: orgId, ticket_id: ticket.id } },
          body: { into_ticket_id: target },
          ...withIdempotencyKey(idem.key),
        }),
      ),
    onSuccess: async () => {
      setOpen(false)
      await invalidate()
      toast.info(t`Tickets merged.`)
    },
    onError: () => toast.error(t`Could not merge the tickets. Please try again.`),
  })
  if (ticket.merged_into_id) return null
  return (
    <>
      <Button type="button" variant="outline" onClick={() => setOpen(true)}>
        <GitMergeIcon aria-hidden="true" />
        <Trans>Merge into another ticket</Trans>
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              <Trans>Merge duplicate</Trans>
            </DialogTitle>
            <DialogDescription>
              <Trans>
                This ticket is closed as a duplicate of the one you choose. Its +1 votes carry over.
              </Trans>
            </DialogDescription>
          </DialogHeader>
          <Label htmlFor="merge-target">
            <Trans>Keep this ticket</Trans>
          </Label>
          <Select id="merge-target" value={target} onChange={(e) => setTarget(e.target.value)}>
            <option value="">{t`Choose a ticket`}</option>
            {candidates.data
              ?.filter((c) => c.id !== ticket.id)
              .map((c) => (
                <option key={c.id} value={c.id}>
                  {c.title}
                </option>
              ))}
          </Select>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setOpen(false)}>
              <Trans>Cancel</Trans>
            </Button>
            <Button
              type="button"
              disabled={!target || mutation.isPending}
              onClick={() => mutation.mutate()}
            >
              <Trans>Merge</Trans>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
