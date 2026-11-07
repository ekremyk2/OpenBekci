import { useMemo, useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { Trans, useLingui } from '@lingui/react/macro'
import { LockIcon } from 'lucide-react'
import {
  apiDraft,
  type Ticket,
  type TicketComment,
  type TicketStatus,
  type TicketStatusChange,
} from '@/api/draft'
import { SiteDate } from '@/components/data/SiteDate'
import { AttachmentGallery } from '@/components/files/AttachmentGallery'
import { UploadField } from '@/components/files/UploadField'
import { useUploads } from '@/components/files/useUploads'
import { Badge } from '@/components/ui/badge'
import { Select } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { SubmitButton } from '@/components/forms/FormField'
import { useIdempotencyKey, withIdempotencyKey } from '@/lib/idempotency'
import { must } from '@/lib/pagination'
import { toast } from '@/lib/toast'
import { useInvalidateTickets } from './TicketActions'

type Entry =
  | { kind: 'comment'; at: string; comment: TicketComment }
  | { kind: 'status'; at: string; change: TicketStatusChange }

/** Status history and comments merged into one timeline, oldest first. */
export function Timeline({
  orgId,
  timeZone,
  comments,
  history,
  statuses,
}: {
  orgId: string
  timeZone: string
  comments: TicketComment[]
  history: TicketStatusChange[]
  statuses: TicketStatus[]
}) {
  const { t } = useLingui()
  const names = useMemo(() => new Map(statuses.map((s) => [s.id, s.name])), [statuses])
  const entries = useMemo<Entry[]>(
    () =>
      [
        ...comments.map((comment) => ({
          kind: 'comment' as const,
          at: comment.created_at,
          comment,
        })),
        ...history.map((change) => ({ kind: 'status' as const, at: change.at, change })),
      ].sort((a, b) => a.at.localeCompare(b.at)),
    [comments, history],
  )
  if (entries.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        <Trans>No activity yet.</Trans>
      </p>
    )
  }
  return (
    <ol className="flex flex-col gap-3" aria-label={t`Timeline`}>
      {entries.map((e) =>
        e.kind === 'status' ? (
          <li key={`s-${e.change.id}`} className="text-sm text-muted-foreground">
            <span>
              {e.change.from_status_id ? (
                <Trans>
                  Status changed from {names.get(e.change.from_status_id) ?? '?'} to{' '}
                  {names.get(e.change.to_status_id) ?? '?'}
                </Trans>
              ) : (
                <Trans>Opened as {names.get(e.change.to_status_id) ?? '?'}</Trans>
              )}
            </span>{' '}
            <SiteDate value={e.change.at} timeZone={timeZone} kind="datetime" />
            {e.change.comment ? <p className="mt-1 text-foreground">{e.change.comment}</p> : null}
          </li>
        ) : (
          <li
            key={`c-${e.comment.id}`}
            className={
              e.comment.visibility === 'internal'
                ? 'rounded-lg border border-dashed border-warning/60 bg-warning/5 p-3'
                : 'rounded-lg border p-3'
            }
          >
            <div className="flex flex-wrap items-center gap-2 text-sm">
              <span className="font-medium">{e.comment.author_display_name}</span>
              <SiteDate
                value={e.comment.created_at}
                timeZone={timeZone}
                kind="datetime"
                className="text-xs text-muted-foreground"
              />
              {e.comment.visibility === 'internal' ? (
                <Badge variant="warning">
                  <LockIcon aria-hidden="true" />
                  <Trans>Internal note</Trans>
                </Badge>
              ) : null}
            </div>
            <p className="mt-1 text-sm whitespace-pre-wrap">{e.comment.body}</p>
            <div className="mt-2">
              <AttachmentGallery
                orgId={orgId}
                items={e.comment.attachments.map((a) => ({
                  id: a.id,
                  fileId: a.file_id,
                  mime: a.mime,
                }))}
              />
            </div>
          </li>
        ),
      )}
    </ol>
  )
}

/** Add a comment (public, or an internal note for staff), with up to 10 photos. */
export function CommentForm({
  orgId,
  siteId,
  ticket,
  canInternal,
}: {
  orgId: string
  siteId: string
  ticket: Ticket
  canInternal: boolean
}) {
  const { t } = useLingui()
  const invalidate = useInvalidateTickets()
  const idem = useIdempotencyKey()
  const [body, setBody] = useState('')
  const [visibility, setVisibility] = useState<'public' | 'internal'>('public')
  const uploads = useUploads({
    orgId,
    siteId,
    scope: 'ticket_comment',
    profile: 'photo',
    maxFiles: 10,
    downscale: true,
  })
  const mutation = useMutation({
    mutationFn: async () =>
      must(
        await apiDraft.POST('/v1/orgs/{org_id}/tickets/{ticket_id}/comments', {
          params: { path: { org_id: orgId, ticket_id: ticket.id } },
          body: {
            body: body.trim(),
            visibility: canInternal ? visibility : undefined,
            attachment_file_ids: uploads.files.map((f) => f.id),
          },
          ...withIdempotencyKey(idem.key),
        }),
      ),
    onSuccess: async () => {
      setBody('')
      uploads.clear()
      idem.renew()
      await invalidate()
    },
    onError: () => toast.error(t`Could not post the comment. Please try again.`),
  })
  return (
    <form
      className="flex flex-col gap-3"
      onSubmit={(e) => {
        e.preventDefault()
        if (body.trim() && !uploads.busy) mutation.mutate()
      }}
    >
      <Textarea
        aria-label={t`Comment`}
        placeholder={t`Write a comment`}
        value={body}
        onChange={(e) => setBody(e.target.value)}
        rows={3}
      />
      <UploadField
        controller={uploads}
        orgId={orgId}
        label={<Trans>Add photos</Trans>}
        hint={<Trans>Up to 10 photos. Location data is removed.</Trans>}
      />
      <div className="flex flex-wrap items-center gap-2">
        {canInternal ? (
          <Select
            aria-label={t`Comment visibility`}
            className="w-auto"
            value={visibility}
            onChange={(e) => setVisibility(e.target.value as 'public' | 'internal')}
          >
            <option value="public">{t`Visible to the reporter`}</option>
            <option value="internal">{t`Internal note`}</option>
          </Select>
        ) : null}
        <SubmitButton busy={mutation.isPending} disabled={!body.trim() || uploads.busy}>
          <Trans>Post comment</Trans>
        </SubmitButton>
      </div>
    </form>
  )
}
