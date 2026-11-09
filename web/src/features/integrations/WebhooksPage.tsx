import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Trans, useLingui } from '@lingui/react/macro'
import { PlusIcon, WebhookIcon } from 'lucide-react'
import { draftApi, ifMatchOf, type Schema } from '@/api/draft'
import { AppError } from '@/api/errors'
import { runWithReauth } from '@/app/reauth'
import { useSession } from '@/app/session'
import { ConfirmDialog } from '@/components/data/ConfirmDialog'
import { EmptyState } from '@/components/data/EmptyState'
import { ErrorState } from '@/components/data/ErrorState'
import { TableSkeleton } from '@/components/data/Skeletons'
import { FormError, FormField, SubmitButton } from '@/components/forms/FormField'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { withIdempotencyKey } from '@/lib/idempotency'
import { problemMessage } from '@/lib/labels'
import { LoadMore, must, useCursorQuery } from '@/lib/pagination'
import { KNOWN_EVENT_TYPES, WEBHOOK_EVENTS } from './events'
import { formatInstant } from './format'
import { SecretRevealDialog } from './SecretReveal'

type Endpoint = Schema<'WebhookEndpoint'>
type Delivery = Schema<'WebhookDelivery'>

const listKey = (orgId: string) => ['integrations.listWebhookEndpoints', orgId] as const
const deliveriesKey = (orgId: string, endpointId: string) =>
  ['integrations.listWebhookDeliveries', orgId, endpointId] as const

/** Webhook endpoints of the organization (permissions `webhooks.read`, `webhooks.manage`). */
export function WebhooksPage({
  endpointId,
  onSelect,
}: {
  /** The endpoint whose deliveries are shown (kept in the URL). */
  endpointId: string | undefined
  onSelect: (endpointId: string | undefined) => void
}) {
  const { org, can } = useSession()
  const { t, i18n } = useLingui()
  const qc = useQueryClient()
  const orgId = org?.id ?? ''
  const canManage = can('webhooks.manage')
  const [editing, setEditing] = useState<Endpoint | 'new' | null>(null)
  const [deleting, setDeleting] = useState<Endpoint | null>(null)
  const [rotating, setRotating] = useState<Endpoint | null>(null)
  const [tested, setTested] = useState<{ endpoint: Endpoint; delivery: Delivery } | null>(null)
  const [revealed, setRevealed] = useState<{
    secret: string
    url: string
    created: boolean
    previousExpiresAt?: string
  } | null>(null)

  const endpoints = useCursorQuery<Endpoint>(
    listKey(orgId),
    async (cursor) =>
      must(
        await draftApi.GET('/v1/orgs/{org_id}/webhook-endpoints', {
          params: { path: { org_id: orgId }, query: { limit: 50, cursor } },
        }),
      ),
    { enabled: !!orgId && (can('webhooks.read') || canManage) },
  )
  const selected = endpoints.items.find((e) => e.id === endpointId)

  const test = useMutation({
    mutationFn: async (endpoint: Endpoint) => ({
      endpoint,
      delivery: must(
        await draftApi.POST('/v1/orgs/{org_id}/webhook-endpoints/{endpoint_id}:test', {
          params: { path: { org_id: orgId, endpoint_id: endpoint.id } },
        }),
      ),
    }),
    onSuccess: async (res) => {
      setTested(res)
      await qc.invalidateQueries({ queryKey: deliveriesKey(orgId, res.endpoint.id) })
    },
  })
  // gcTime 0: the answer holds the one-time secret, so the mutation cache must not keep it
  const rotate = useMutation({
    gcTime: 0,
    mutationFn: (endpoint: Endpoint) =>
      runWithReauth(async () =>
        must(
          await draftApi.POST('/v1/orgs/{org_id}/webhook-endpoints/{endpoint_id}:rotate-secret', {
            params: { path: { org_id: orgId, endpoint_id: endpoint.id } },
          }),
        ),
      ),
    onSuccess: (data) => {
      setRotating(null)
      setRevealed({
        secret: data.secret,
        url: data.endpoint.url,
        created: false,
        previousExpiresAt: data.previous_secret_expires_at,
      })
    },
  })
  const remove = useMutation({
    mutationFn: async (endpoint: Endpoint) =>
      draftApi.DELETE('/v1/orgs/{org_id}/webhook-endpoints/{endpoint_id}', {
        params: { path: { org_id: orgId, endpoint_id: endpoint.id } },
        ...withIdempotencyKey(crypto.randomUUID()),
      }),
    onSuccess: async (_d, endpoint) => {
      setDeleting(null)
      if (endpointId === endpoint.id) onSelect(undefined)
      await qc.invalidateQueries({ queryKey: listKey(orgId) })
    },
  })

  const actionError = (e: unknown) =>
    e instanceof AppError && !e.isReauthRequired
      ? (problemMessage(e.code, i18n) ??
        (e.isForbidden
          ? t`You do not have permission to do this.`
          : t`Something went wrong. Please try again.`))
      : undefined

  return (
    <section className="flex flex-col gap-4" aria-labelledby="hooks-title">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex flex-col gap-1">
          <h2 id="hooks-title" className="text-lg font-semibold">
            <Trans>Webhooks</Trans>
          </h2>
          <p className="max-w-prose text-sm text-muted-foreground">
            <Trans>
              OpenBekci calls your https address when something happens. Each call is signed with
              the endpoint&apos;s secret and carries ids only.
            </Trans>
          </p>
        </div>
        {canManage ? (
          <Button onClick={() => setEditing('new')}>
            <PlusIcon aria-hidden="true" />
            <Trans>Add endpoint</Trans>
          </Button>
        ) : null}
      </div>

      {endpoints.isPending ? <TableSkeleton columns={4} /> : null}
      {endpoints.isError ? (
        <ErrorState error={endpoints.error} onRetry={() => void endpoints.refetch()} />
      ) : null}
      {endpoints.data && endpoints.items.length === 0 ? (
        <EmptyState
          icon={WebhookIcon}
          title={<Trans>No webhook endpoints yet</Trans>}
          description={<Trans>Add an endpoint to be called when events happen.</Trans>}
        />
      ) : null}
      {test.isError ? <FormError message={actionError(test.error)} /> : null}
      {endpoints.items.length > 0 ? (
        <ul aria-label={t`Webhook endpoints`} className="flex flex-col gap-3">
          {endpoints.items.map((e) => (
            <li key={e.id}>
              <Card className={selected?.id === e.id ? 'border-primary' : undefined}>
                <CardHeader className="gap-2">
                  <CardTitle className="flex flex-wrap items-center gap-2 text-base">
                    <code className="text-sm break-all" dir="ltr">
                      {e.url}
                    </code>
                    {e.active ? (
                      <Badge variant="success">
                        <Trans>Active</Trans>
                      </Badge>
                    ) : (
                      <Badge variant="secondary">
                        <Trans>Inactive</Trans>
                      </Badge>
                    )}
                    {e.failing ? (
                      <Badge variant="destructive">
                        <Trans>Failing since {formatInstant(e.failing_since, i18n.locale)}</Trans>
                      </Badge>
                    ) : null}
                  </CardTitle>
                </CardHeader>
                <CardContent className="flex flex-col gap-3">
                  {e.description ? <p className="text-sm">{e.description}</p> : null}
                  <ul className="flex flex-wrap gap-1" aria-label={t`Events`}>
                    {e.events.map((ev) => (
                      <li key={ev}>
                        <Badge variant="secondary" className="font-mono">
                          {ev}
                        </Badge>
                      </li>
                    ))}
                  </ul>
                  <div className="flex flex-wrap gap-2">
                    <Button
                      variant={selected?.id === e.id ? 'default' : 'outline'}
                      size="sm"
                      aria-label={t`Deliveries of ${e.url}`}
                      onClick={() => onSelect(selected?.id === e.id ? undefined : e.id)}
                    >
                      <Trans>Deliveries</Trans>
                    </Button>
                    {canManage ? (
                      <>
                        <Button
                          variant="outline"
                          size="sm"
                          disabled={test.isPending}
                          aria-label={t`Send a test ping to ${e.url}`}
                          onClick={() => test.mutate(e)}
                        >
                          <Trans>Send test</Trans>
                        </Button>
                        <Button
                          variant="outline"
                          size="sm"
                          aria-label={t`Edit ${e.url}`}
                          onClick={() => setEditing(e)}
                        >
                          <Trans>Edit</Trans>
                        </Button>
                        <Button
                          variant="outline"
                          size="sm"
                          aria-label={t`Rotate the secret of ${e.url}`}
                          onClick={() => setRotating(e)}
                        >
                          <Trans>Rotate secret</Trans>
                        </Button>
                        <Button
                          variant="outline"
                          size="sm"
                          aria-label={t`Delete ${e.url}`}
                          onClick={() => setDeleting(e)}
                        >
                          <Trans>Delete</Trans>
                        </Button>
                      </>
                    ) : null}
                  </div>
                </CardContent>
              </Card>
            </li>
          ))}
        </ul>
      ) : null}
      <LoadMore query={endpoints} />

      {selected ? <DeliveriesPanel orgId={orgId} endpoint={selected} /> : null}

      {editing ? (
        <EndpointDialog
          orgId={orgId}
          endpoint={editing === 'new' ? null : editing}
          onClose={() => setEditing(null)}
          onSaved={async (secret) => {
            const wasNew = editing === 'new'
            setEditing(null)
            await qc.invalidateQueries({ queryKey: listKey(orgId) })
            if (wasNew && secret) {
              setRevealed({ secret: secret.secret, url: secret.endpoint.url, created: true })
            }
          }}
        />
      ) : null}

      {tested ? (
        <Dialog open onOpenChange={(o) => !o && setTested(null)}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>
                <Trans>Test ping sent</Trans>
              </DialogTitle>
              <DialogDescription>
                <Trans>
                  A ping event was queued for <code dir="ltr">{tested.endpoint.url}</code>.
                </Trans>
              </DialogDescription>
            </DialogHeader>
            <DeliveryResult delivery={tested.delivery} locale={i18n.locale} />
            <DialogFooter>
              <Button variant="outline" onClick={() => setTested(null)}>
                <Trans>Close</Trans>
              </Button>
              <Button
                onClick={() => {
                  onSelect(tested.endpoint.id)
                  setTested(null)
                }}
              >
                <Trans>Show deliveries</Trans>
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      ) : null}

      <ConfirmDialog
        open={!!rotating}
        onOpenChange={(o) => !o && setRotating(null)}
        busy={rotate.isPending}
        title={<Trans>Rotate the secret?</Trans>}
        description={
          <Trans>
            A new secret is issued and shown once. The old one keeps signing for 24 hours next to
            the new one, so your receiver can switch without a gap.
          </Trans>
        }
        confirmLabel={<Trans>Rotate secret</Trans>}
        onConfirm={() => rotating && rotate.mutate(rotating)}
      >
        <FormError message={actionError(rotate.error)} />
      </ConfirmDialog>
      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(o) => !o && setDeleting(null)}
        destructive
        busy={remove.isPending}
        title={<Trans>Delete this endpoint?</Trans>}
        description={
          <Trans>
            The endpoint and its delivery log are removed and nothing is sent to it any more.
          </Trans>
        }
        confirmLabel={<Trans>Delete endpoint</Trans>}
        onConfirm={() => deleting && remove.mutate(deleting)}
      >
        <FormError message={actionError(remove.error)} />
      </ConfirmDialog>

      {revealed ? (
        <SecretRevealDialog
          open
          secret={revealed.secret}
          title={
            revealed.created ? <Trans>Signing secret</Trans> : <Trans>New signing secret</Trans>
          }
          description={
            <Trans>
              Use it to verify the <code>OpenBekci-Signature</code> header of calls to{' '}
              <code dir="ltr">{revealed.url}</code>. It is kept sealed on the server.
            </Trans>
          }
          extra={
            revealed.previousExpiresAt ? (
              <p className="rounded-md border bg-muted/40 p-3 text-sm">
                <Trans>
                  The previous secret keeps signing until{' '}
                  <strong>{formatInstant(revealed.previousExpiresAt, i18n.locale)}</strong>. Every
                  call carries both signatures until then.
                </Trans>
              </p>
            ) : null
          }
          onDone={() => {
            setRevealed(null)
            rotate.reset() // the observer still holds the rotate answer, secret included
          }}
        />
      ) : null}
    </section>
  )
}

/** A full https address with a host (the server still refuses non-public targets). */
export function isHttpsUrl(value: string): boolean {
  try {
    const u = new URL(value.trim())
    return u.protocol === 'https:' && u.hostname !== '' && !u.username && !u.password
  } catch {
    return false
  }
}

export function DeliveryStatusBadge({ status }: { status: string }) {
  return status === 'succeeded' ? (
    <Badge variant="success">
      <Trans>Succeeded</Trans>
    </Badge>
  ) : status === 'failed' ? (
    <Badge variant="destructive">
      <Trans>Failed</Trans>
    </Badge>
  ) : status === 'pending' ? (
    <Badge variant="warning">
      <Trans>Pending</Trans>
    </Badge>
  ) : (
    <Badge variant="secondary">{status}</Badge>
  )
}

function DeliveryResult({ delivery, locale }: { delivery: Delivery; locale: string }) {
  return (
    <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
      <dt className="text-muted-foreground">
        <Trans>Status</Trans>
      </dt>
      <dd>
        <DeliveryStatusBadge status={delivery.status} />
      </dd>
      <dt className="text-muted-foreground">
        <Trans>Attempts</Trans>
      </dt>
      <dd>{delivery.attempts}</dd>
      <dt className="text-muted-foreground">
        <Trans>Response</Trans>
      </dt>
      <dd>
        {delivery.response_code ?? '—'}
        {delivery.last_error ? (
          <span className="ms-2 font-mono text-xs">{delivery.last_error}</span>
        ) : null}
      </dd>
      {delivery.next_retry_at ? (
        <>
          <dt className="text-muted-foreground">
            <Trans>Next retry</Trans>
          </dt>
          <dd>{formatInstant(delivery.next_retry_at, locale)}</dd>
        </>
      ) : null}
    </dl>
  )
}

/* ───────────── deliveries ───────────── */

function DeliveriesPanel({ orgId, endpoint }: { orgId: string; endpoint: Endpoint }) {
  const { t, i18n } = useLingui()
  const [status, setStatus] = useState('')
  const deliveries = useCursorQuery<Delivery>(
    [...deliveriesKey(orgId, endpoint.id), status],
    async (cursor) =>
      must(
        await draftApi.GET('/v1/orgs/{org_id}/webhook-endpoints/{endpoint_id}/deliveries', {
          params: {
            path: { org_id: orgId, endpoint_id: endpoint.id },
            query: {
              limit: 25,
              cursor,
              ...(status ? { status: status as 'pending' | 'succeeded' | 'failed' } : {}),
            },
          },
        }),
      ),
    // a delivery waiting for a retry changes without anyone clicking
    { refetchInterval: 15_000 },
  )
  return (
    <section
      aria-labelledby="deliveries-title"
      className="flex flex-col gap-3 rounded-lg border p-4"
    >
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h3 id="deliveries-title" className="text-base font-semibold">
          <Trans>
            Deliveries of <code dir="ltr">{endpoint.url}</code>
          </Trans>
        </h3>
        <div className="flex items-center gap-2">
          <label htmlFor="delivery-status" className="text-sm">
            <Trans>Status</Trans>
          </label>
          <Select
            id="delivery-status"
            className="w-40"
            value={status}
            onChange={(e) => setStatus(e.target.value)}
          >
            <option value="">{t`All`}</option>
            <option value="pending">{t`Pending`}</option>
            <option value="succeeded">{t`Succeeded`}</option>
            <option value="failed">{t`Failed`}</option>
          </Select>
        </div>
      </div>
      {deliveries.isPending ? <TableSkeleton columns={6} /> : null}
      {deliveries.isError ? (
        <ErrorState error={deliveries.error} onRetry={() => void deliveries.refetch()} />
      ) : null}
      {deliveries.data && deliveries.items.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          <Trans>Nothing has been sent to this endpoint yet.</Trans>
        </p>
      ) : null}
      {deliveries.items.length > 0 ? (
        <div className="overflow-x-auto rounded-md border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>
                  <Trans>Event</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Status</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Attempts</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Response</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Next retry</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Created</Trans>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {deliveries.items.map((d) => (
                <TableRow key={d.id}>
                  <TableCell>
                    <code className="text-xs">{d.event_type}</code>
                  </TableCell>
                  <TableCell>
                    <DeliveryStatusBadge status={d.status} />
                  </TableCell>
                  <TableCell>{d.attempts}</TableCell>
                  <TableCell>
                    {d.response_code ?? '—'}
                    {d.last_error ? (
                      <span className="ms-2 font-mono text-xs text-muted-foreground">
                        {d.last_error}
                      </span>
                    ) : null}
                  </TableCell>
                  <TableCell>
                    {d.next_retry_at ? formatInstant(d.next_retry_at, i18n.locale) : '—'}
                  </TableCell>
                  <TableCell>{formatInstant(d.created_at, i18n.locale)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      ) : null}
      <LoadMore query={deliveries} />
    </section>
  )
}

/* ───────────── create and edit ───────────── */

function EndpointDialog({
  orgId,
  endpoint,
  onClose,
  onSaved,
}: {
  orgId: string
  endpoint: Endpoint | null
  onClose: () => void
  onSaved: (created?: Schema<'WebhookEndpointCreated'>) => void | Promise<void>
}) {
  const { t, i18n } = useLingui()
  const [url, setUrl] = useState(endpoint?.url ?? 'https://')
  const [description, setDescription] = useState(endpoint?.description ?? '')
  const [events, setEvents] = useState<string[]>(endpoint?.events ?? [])
  const [active, setActive] = useState(endpoint?.active ?? true)
  const [local, setLocal] = useState<Record<string, string>>({})

  const qc = useQueryClient()
  const save = useMutation({
    gcTime: 0, // a create answer holds the one-time secret
    mutationFn: async () => {
      if (!endpoint) {
        return must(
          await draftApi.POST('/v1/orgs/{org_id}/webhook-endpoints', {
            params: { path: { org_id: orgId } },
            body: {
              url: url.trim(),
              events,
              description: description.trim() || null,
              active,
            },
          }),
        )
      }
      // a partial update: only what changed, guarded by the version the form was opened on
      const body: Schema<'WebhookEndpointUpdate'> = {}
      if (url.trim() !== endpoint.url) body.url = url.trim()
      if (JSON.stringify([...events].sort()) !== JSON.stringify([...endpoint.events].sort()))
        body.events = events
      if ((description.trim() || null) !== (endpoint.description ?? null))
        body.description = description.trim() || null
      if (active !== endpoint.active) body.active = active
      if (Object.keys(body).length === 0) return endpoint // nothing changed: no request
      return must(
        await draftApi.PATCH('/v1/orgs/{org_id}/webhook-endpoints/{endpoint_id}', {
          params: {
            path: { org_id: orgId, endpoint_id: endpoint.id },
            header: ifMatchOf(endpoint.version),
          },
          body,
        }),
      )
    },
    onSuccess: (data) => onSaved('secret' in data ? data : undefined),
    onError: (e) => {
      // a 409 means the list is out of date: reload it so reopening the endpoint shows the new one
      if (e instanceof AppError && e.isVersionConflict) {
        void qc.invalidateQueries({ queryKey: listKey(orgId) })
      }
    },
  })

  const remote: Record<string, string> = {}
  if (save.error instanceof AppError && save.error.isValidation) {
    for (const [field, issues] of Object.entries(save.error.fieldErrors)) {
      const issue = issues[0]
      if (!issue) continue
      const key = field.replace(/\[\d+\]$/, '')
      remote[key] =
        issue.message ??
        (issue.code === 'required'
          ? t`This field is required`
          : issue.code === 'forbidden_target'
            ? t`This address is not reachable from the internet`
            : t`This value is not valid`)
    }
  }
  const err = (field: string) => local[field] ?? remote[field]
  const rootError =
    save.error instanceof AppError
      ? save.error.isValidation && Object.keys(remote).length > 0
        ? undefined
        : save.error.isVersionConflict
          ? t`Someone else changed this endpoint. Close this and open it again.`
          : save.error.code === 'conflict'
            ? t`The organization has reached its limit of 20 endpoints.`
            : (problemMessage(save.error.code, i18n) ??
              (save.error.isForbidden
                ? t`You do not have permission to do this.`
                : t`Something went wrong. Please try again.`))
      : save.error
        ? t`Something went wrong. Please try again.`
        : undefined

  function submit(e: React.FormEvent) {
    e.preventDefault()
    const errs: Record<string, string> = {}
    if (!isHttpsUrl(url)) errs.url = t`Enter an address starting with https://`
    if (events.length === 0) errs.events = t`Choose at least one event`
    setLocal(errs)
    if (Object.keys(errs).length > 0) return
    save.mutate()
  }
  const unknown = events.filter((e) => !KNOWN_EVENT_TYPES.has(e))

  return (
    <Dialog open onOpenChange={(o) => !o && !save.isPending && onClose()}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>
            {endpoint ? <Trans>Edit endpoint</Trans> : <Trans>Add endpoint</Trans>}
          </DialogTitle>
          <DialogDescription>
            <Trans>
              Only https addresses that resolve to a public address are accepted. A signing secret
              is shown once after you create the endpoint.
            </Trans>
          </DialogDescription>
        </DialogHeader>
        <form noValidate className="flex flex-col gap-4" onSubmit={submit}>
          <FormField id="hook-url" label={<Trans>URL</Trans>} error={err('url')}>
            <Input
              id="hook-url"
              type="url"
              dir="ltr"
              value={url}
              aria-invalid={!!err('url') || undefined}
              onChange={(e) => setUrl(e.target.value)}
              placeholder="https://hooks.example.org/openbekci"
            />
          </FormField>
          <FormField
            id="hook-description"
            label={<Trans>Description (optional)</Trans>}
            error={err('description')}
          >
            <Input
              id="hook-description"
              maxLength={500}
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder={t`Accounting`}
            />
          </FormField>
          <fieldset className="flex flex-col gap-2" aria-describedby="hook-events-error">
            <legend className="text-sm leading-none font-medium">
              <Trans>Events</Trans>
            </legend>
            <div className="grid gap-2 sm:grid-cols-2">
              {WEBHOOK_EVENTS.map((ev) => (
                <label key={ev.type} className="flex items-start gap-2 text-sm">
                  <Checkbox
                    className="mt-0.5"
                    checked={events.includes(ev.type)}
                    onChange={(e) =>
                      setEvents((s) =>
                        e.target.checked ? [...s, ev.type] : s.filter((x) => x !== ev.type),
                      )
                    }
                  />
                  <span className="flex flex-col">
                    <span>{i18n._(ev.label)}</span>
                    <span className="font-mono text-xs text-muted-foreground" dir="ltr">
                      {ev.type}
                    </span>
                  </span>
                </label>
              ))}
              {unknown.map((ev) => (
                <label key={ev} className="flex items-start gap-2 text-sm">
                  <Checkbox
                    className="mt-0.5"
                    checked
                    onChange={() => setEvents((s) => s.filter((x) => x !== ev))}
                  />
                  <span className="font-mono text-xs" dir="ltr">
                    {ev}
                  </span>
                </label>
              ))}
            </div>
            {err('events') ? (
              <p id="hook-events-error" role="alert" className="text-sm text-destructive">
                {err('events')}
              </p>
            ) : null}
          </fieldset>
          <label className="flex items-start gap-3">
            <Checkbox
              className="mt-1"
              checked={active}
              onChange={(e) => setActive(e.target.checked)}
            />
            <span className="flex flex-col">
              <span className="text-sm font-medium">{t`Active`}</span>
              <span className="text-xs text-muted-foreground">
                {t`An inactive endpoint receives no new events.`}
              </span>
            </span>
          </label>
          <FormError message={rootError} />
          <DialogFooter>
            <Button type="button" variant="outline" disabled={save.isPending} onClick={onClose}>
              <Trans>Cancel</Trans>
            </Button>
            <SubmitButton busy={save.isPending}>
              {endpoint ? <Trans>Save endpoint</Trans> : <Trans>Create endpoint</Trans>}
            </SubmitButton>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
