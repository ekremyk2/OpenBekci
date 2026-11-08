import { useMemo, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Trans, useLingui } from '@lingui/react/macro'
import { PaperclipIcon, SendIcon } from 'lucide-react'
import { AppError } from '@/api/errors'
import { SiteDate } from '@/components/data/SiteDate'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { useIdempotencyKey } from '@/lib/idempotency'
import { toast } from '@/lib/toast'
import { GOV_SCALE, useGovernanceSettings, useNotices } from './hooks'
import {
  gApi,
  idemHeader,
  orgUrl,
  unwrap,
  useKeyStore,
  useOrgMutation,
  type S,
  type SiteCtx,
} from './kit/api'
import { localInputToInstant, instantToLocalInput } from './kit/time'
import {
  applyServerErrors,
  Checkbox,
  Field,
  FormRootError,
  QueryBoundary,
  Select,
  Textarea,
} from './kit/ui'
import { Callout, DownloadButton } from './kit/widgets'
import { callTimingIssues, noticeProgress } from './logic'
import { NOTICE_METHODS, useGovLabels } from './labels'

const CHANNELS = ['in_app', 'email', 'sms'] as const

/** Step 1 (draft): check the legal timing, choose channels, send the call. */
export function CallStep({ ctx, meeting }: { ctx: SiteCtx; meeting: S['Meeting'] }) {
  const { t } = useLingui()
  const idem = useIdempotencyKey()
  const settings = useGovernanceSettings(ctx.orgId, ctx.siteId)
  const [channels, setChannels] = useState<string[]>(['in_app'])
  const [error, setError] = useState<string | null>(null)
  const issues = settings.data
    ? callTimingIssues(settings.data.settings, meeting.scheduled_at, meeting.second_call_at)
    : []
  const call = useOrgMutation(async () =>
    unwrap(
      gApi.POST('/v1/orgs/{org_id}/meetings/{meeting_id}:call', {
        params: {
          path: { org_id: ctx.orgId, meeting_id: meeting.id },
          header: idemHeader(idem.key),
        },
        body: { notify_channels: channels as ('in_app' | 'email' | 'sms')[] },
      }),
    ),
  )
  const names: Record<string, string> = { in_app: t`In the app`, email: t`Email`, sms: t`SMS` }
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <Trans>Send the call</Trans>
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <p className="text-sm text-muted-foreground">
          <Trans>
            Calling the assembly freezes who may vote, generates the call letter and creates a
            delivery checklist with one line per owner.
          </Trans>
        </p>
        {issues.includes('call_too_late') && settings.data ? (
          <Callout tone="warning" role="alert">
            <Trans>
              The first meeting is less than {settings.data.settings.call_min_days} days away. The
              server will refuse a call that is too late; move the date first.
            </Trans>
          </Callout>
        ) : null}
        {issues.includes('second_call_timing') && settings.data ? (
          <Callout tone="warning" role="alert">
            <Trans>
              The second meeting should be {settings.data.settings.second_call_min_days} to{' '}
              {settings.data.settings.second_call_max_days} days after the first.
            </Trans>
          </Callout>
        ) : null}
        <fieldset className="flex flex-wrap gap-4">
          <legend className="mb-1 text-sm font-medium">
            <Trans>Also notify owners</Trans>
          </legend>
          {CHANNELS.map((c) => (
            <Checkbox
              key={c}
              label={names[c]}
              checked={channels.includes(c)}
              onChange={(e) =>
                setChannels((cur) => (e.target.checked ? [...cur, c] : cur.filter((x) => x !== c)))
              }
            />
          ))}
        </fieldset>
        {error ? <FormRootError message={error} /> : null}
        <div>
          <Button
            disabled={call.isPending}
            onClick={() => {
              setError(null)
              call.mutate(undefined, {
                onSuccess: () => toast.info(t`The call has been sent.`),
                onError: (e) =>
                  setError(
                    e instanceof AppError && e.isValidation
                      ? (Object.values(e.fieldErrors)[0]?.[0]?.message ??
                          t`The legal timing is not met.`)
                      : t`Could not send the call.`,
                  ),
              })
            }}
          >
            <SendIcon />
            <Trans>Call the assembly</Trans>
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

/** Step 2: the call letter and the per-owner delivery checklist (G3). */
export function DeliveryChecklist({
  ctx,
  meeting,
  phase = 'call',
}: {
  ctx: SiteCtx
  meeting: S['Meeting']
  phase?: 'call' | 'decisions'
}) {
  const { t } = useLingui()
  const labels = useGovLabels()
  const notices = useNotices(ctx.orgId, meeting.id, phase)
  const [editing, setEditing] = useState<S['MeetingNotice'] | null>(null)
  const [selected, setSelected] = useState<string[]>([])
  const [bulkMethod, setBulkMethod] = useState<string>('signed')
  const bulkKeys = useKeyStore()
  const bulk = useOrgMutation(async () => {
    const id = `bulk-${bulkMethod}-${selected.join(',')}`
    const r = await unwrap(
      gApi.POST('/v1/orgs/{org_id}/meetings/{meeting_id}/notices:bulk', {
        params: {
          path: { org_id: ctx.orgId, meeting_id: meeting.id },
          header: idemHeader(bulkKeys.get(id)),
        },
        body: {
          person_ids: selected,
          phase,
          method: bulkMethod as 'signed',
          sent_at: new Date().toISOString(),
        },
      }),
    )
    bulkKeys.done(id)
    return r
  })
  const progress = noticeProgress(notices.data ?? [])
  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-2">
        <CardTitle>
          {phase === 'call' ? (
            <Trans>Delivery of the call</Trans>
          ) : (
            <Trans>Delivery of the decisions</Trans>
          )}
        </CardTitle>
        {phase === 'call' ? (
          <DownloadButton
            url={orgUrl(ctx.orgId, `/meetings/${meeting.id}/call-letter`)}
            filename={`call-letter-${meeting.id}.pdf`}
          >
            <Trans>Call letter (PDF)</Trans>
          </DownloadButton>
        ) : null}
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <QueryBoundary
          query={notices}
          isEmpty={(d) => d.length === 0}
          emptyTitle={t`No deliveries to record`}
          emptyDescription={
            phase === 'call'
              ? t`The checklist appears when the assembly has been called.`
              : t`Decisions are sent to absent owners after the assembly.`
          }
        >
          {(items) => (
            <>
              <div className="flex flex-col gap-1">
                <div className="flex justify-between text-sm">
                  <span>
                    <Trans>
                      {progress.recorded} of {progress.total} delivered
                    </Trans>
                  </span>
                  <span className="tabular-nums">{progress.pct}%</span>
                </div>
                <div
                  role="progressbar"
                  aria-label={t`Delivery progress`}
                  aria-valuemin={0}
                  aria-valuemax={100}
                  aria-valuenow={progress.pct}
                  className="h-2 overflow-hidden rounded-full bg-muted"
                >
                  <div
                    className="h-full bg-success"
                    ref={(el) => el?.style.setProperty('width', `${progress.pct}%`)}
                  />
                </div>
              </div>
              <div className="flex flex-wrap items-center gap-2">
                <Select
                  aria-label={t`Method for the selected owners`}
                  className="w-auto"
                  value={bulkMethod}
                  onChange={(e) => setBulkMethod(e.target.value)}
                >
                  {NOTICE_METHODS.map((m) => (
                    <option key={m} value={m}>
                      {labels.noticeMethod(m)}
                    </option>
                  ))}
                </Select>
                <Button
                  size="sm"
                  variant="secondary"
                  disabled={selected.length === 0 || bulk.isPending}
                  onClick={() =>
                    bulk.mutate(undefined, {
                      onSuccess: () => {
                        setSelected([])
                        toast.info(t`Deliveries recorded.`)
                      },
                      onError: () => toast.error(t`Could not record the deliveries.`),
                    })
                  }
                >
                  <Trans>Record for selected today</Trans>
                </Button>
              </div>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-8">
                      <span className="sr-only">
                        <Trans>Select</Trans>
                      </span>
                    </TableHead>
                    <TableHead>
                      <Trans>Owner</Trans>
                    </TableHead>
                    <TableHead>
                      <Trans>Method</Trans>
                    </TableHead>
                    <TableHead>
                      <Trans>Sent</Trans>
                    </TableHead>
                    <TableHead>
                      <Trans>Tracking no.</Trans>
                    </TableHead>
                    <TableHead>
                      <Trans>Proof</Trans>
                    </TableHead>
                    <TableHead>
                      <span className="sr-only">
                        <Trans>Actions</Trans>
                      </span>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {items.map((n) => (
                    <TableRow key={n.id}>
                      <TableCell>
                        <input
                          type="checkbox"
                          className="size-4 accent-primary"
                          aria-label={t`Select ${n.person_display_name}`}
                          checked={selected.includes(n.person_id)}
                          disabled={n.status === 'recorded'}
                          onChange={(e) =>
                            setSelected((cur) =>
                              e.target.checked
                                ? [...cur, n.person_id]
                                : cur.filter((x) => x !== n.person_id),
                            )
                          }
                        />
                      </TableCell>
                      <TableCell className="font-medium">{n.person_display_name}</TableCell>
                      <TableCell>
                        {n.status === 'recorded' ? (
                          labels.noticeMethod(n.method)
                        ) : (
                          <Badge variant="warning">
                            <Trans>Pending</Trans>
                          </Badge>
                        )}
                      </TableCell>
                      <TableCell>
                        {n.sent_at ? <SiteDate value={n.sent_at} timeZone={ctx.timeZone} /> : '–'}
                      </TableCell>
                      <TableCell dir="ltr">{n.tracking_no ?? '–'}</TableCell>
                      <TableCell>
                        {n.proof_file_id ? (
                          <PaperclipIcon className="size-4" aria-label={t`Proof attached`} />
                        ) : (
                          '–'
                        )}
                      </TableCell>
                      <TableCell>
                        <Button size="sm" variant="outline" onClick={() => setEditing(n)}>
                          {n.status === 'recorded' ? t`Edit` : t`Record`}
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </>
          )}
        </QueryBoundary>
        {editing ? (
          <NoticeDialog
            ctx={ctx}
            meeting={meeting}
            notice={editing}
            onClose={() => setEditing(null)}
          />
        ) : null}
      </CardContent>
    </Card>
  )
}

/** Records how and when one owner received the call: method, date, tracking number, proof file. */
export function NoticeDialog({
  ctx,
  meeting,
  notice,
  onClose,
}: {
  ctx: SiteCtx
  meeting: S['Meeting']
  notice: S['MeetingNotice']
  onClose: () => void
}) {
  const { t } = useLingui()
  const labels = useGovLabels()
  const idem = useIdempotencyKey()
  const [file, setFile] = useState<File | null>(null)
  const schema = useMemo(
    () =>
      z
        .object({
          method: z.enum(['registered_mail', 'signed', 'email', 'sms', 'other']),
          sent_on: z.string().min(1, t`Enter the date it was sent.`),
          tracking_no: z.string().trim(),
          note: z.string(),
        })
        .refine((v) => v.method !== 'registered_mail' || v.tracking_no !== '', {
          path: ['tracking_no'],
          message: t`Registered mail needs the tracking number.`,
        }),
    [t],
  )
  type Values = z.infer<typeof schema>
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: {
      method: notice.method ?? 'registered_mail',
      sent_on: notice.sent_at
        ? instantToLocalInput(notice.sent_at, ctx.timeZone).slice(0, 10)
        : instantToLocalInput(new Date().toISOString(), ctx.timeZone).slice(0, 10),
      tracking_no: notice.tracking_no ?? '',
      note: notice.note ?? '',
    },
  })
  const method = useWatch({ control: form.control, name: 'method' })
  const save = useOrgMutation(async (v: Values) => {
    let proof = notice.proof_file_id ?? undefined
    if (file) {
      const fd = new FormData()
      fd.append('file', file)
      fd.append('site_id', ctx.siteId)
      fd.append('scope', GOV_SCALE)
      const up = await unwrap(
        gApi.POST('/v1/orgs/{org_id}/files', {
          params: { path: { org_id: ctx.orgId }, header: idemHeader(`${idem.key}-file`) },
          body: fd as unknown as { file: string },
          bodySerializer: (b: unknown) => b as FormData,
        }),
      )
      proof = up.id
    }
    return unwrap(
      gApi.PUT('/v1/orgs/{org_id}/meetings/{meeting_id}/notices/{person_id}', {
        params: {
          path: { org_id: ctx.orgId, meeting_id: meeting.id, person_id: notice.person_id },
        },
        headers: idemHeader(idem.key),
        body: {
          phase: notice.phase,
          method: v.method,
          sent_at: localInputToInstant(`${v.sent_on}T12:00`, ctx.timeZone),
          ...(v.tracking_no ? { tracking_no: v.tracking_no } : {}),
          ...(proof ? { proof_file_id: proof } : {}),
          ...(v.note ? { note: v.note } : {}),
        },
      }),
    )
  })
  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : undefined)}>
      <DialogContent>
        <form
          className="flex flex-col gap-4"
          noValidate
          onSubmit={form.handleSubmit(async (v) => {
            try {
              await save.mutateAsync(v)
              toast.info(t`Delivery recorded.`)
              onClose()
            } catch (e) {
              applyServerErrors(form, e, t`Could not record the delivery.`)
            }
          })}
        >
          <DialogHeader>
            <DialogTitle>
              <Trans>Delivery to {notice.person_display_name}</Trans>
            </DialogTitle>
            <DialogDescription>
              <Trans>Keep the proof: this record is what counts if a decision is challenged.</Trans>
            </DialogDescription>
          </DialogHeader>
          <Field label={t`Method`} htmlFor="nt-method">
            <Select id="nt-method" {...form.register('method')}>
              {[...NOTICE_METHODS, 'other' as const].map((m) => (
                <option key={m} value={m}>
                  {labels.noticeMethod(m)}
                </option>
              ))}
            </Select>
          </Field>
          <Field
            label={t`Sent on`}
            htmlFor="nt-date"
            error={form.formState.errors.sent_on?.message}
          >
            <Input id="nt-date" type="date" dir="ltr" {...form.register('sent_on')} />
          </Field>
          <Field
            label={
              method === 'registered_mail' ? t`Tracking number` : t`Tracking number (optional)`
            }
            htmlFor="nt-track"
            error={form.formState.errors.tracking_no?.message}
          >
            <Input id="nt-track" dir="ltr" {...form.register('tracking_no')} />
          </Field>
          <Field label={t`Proof (photo or PDF)`} htmlFor="nt-proof">
            <Input
              id="nt-proof"
              type="file"
              accept="image/*,application/pdf"
              onChange={(e) => setFile(e.target.files?.[0] ?? null)}
            />
          </Field>
          <Field label={t`Note`} htmlFor="nt-note">
            <Textarea id="nt-note" {...form.register('note')} />
          </Field>
          <FormRootError message={form.formState.errors.root?.message} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              <Trans>Cancel</Trans>
            </Button>
            <Button type="submit" disabled={save.isPending}>
              <Trans>Save</Trans>
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
