import { useState } from 'react'
import { CheckIcon, CopyIcon, PlusIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import { SiteDate } from '@/components/data/SiteDate'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
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
import {
  gApi,
  idemHeader,
  unwrap,
  useKeyStore,
  useOrgMutation,
  useOrgQuery,
  type S,
  type SiteCtx,
} from '@/features/meetings/kit/api'
import { ConfirmDialog, Field, FormRootError, QueryBoundary } from '@/features/meetings/kit/ui'
import { Callout } from '@/features/meetings/kit/widgets'
import { copyText } from '@/lib/clipboard'

export function useAgents(ctx: SiteCtx) {
  return useOrgQuery(
    ['agents', ctx.siteId],
    () =>
      unwrap(
        gApi.GET('/v1/orgs/{org_id}/agents', {
          params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, limit: 100 } },
        }),
      ).then((r) => r.items),
    { refetchInterval: 15_000 },
  )
}

/** The enrolment token of a new agent: shown once, never again (it is a bearer secret). */
export function EnrolTokenDialog({
  enrolment,
  ctx,
  onClose,
}: {
  enrolment: S['AgentEnrolment']
  ctx: SiteCtx
  onClose: () => void
}) {
  const { t } = useLingui()
  const [copied, setCopied] = useState(false)
  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : undefined)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            <Trans>Enrolment token for {enrolment.agent.name}</Trans>
          </DialogTitle>
          <DialogDescription>
            <Trans>
              Enter this token once when you install the agent on the gate computer. It expires{' '}
              <SiteDate
                value={enrolment.enrolment_expires_at}
                timeZone={ctx.timeZone}
                kind="datetime"
              />
              .
            </Trans>
          </DialogDescription>
        </DialogHeader>
        <code
          className="break-all rounded bg-muted p-3 text-sm"
          dir="ltr"
          data-testid="enrol-token"
        >
          {enrolment.enrolment_token}
        </code>
        <Callout tone="warning" role="status">
          <Trans>
            This token is shown only now. If you lose it, re-enrol the agent to get a new one.
          </Trans>
        </Callout>
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() =>
              void copyText(enrolment.enrolment_token).then((ok) => ok && setCopied(true))
            }
          >
            {copied ? <CheckIcon /> : <CopyIcon />}
            {copied ? t`Copied` : t`Copy the token`}
          </Button>
          <Button onClick={onClose}>
            <Trans>I have saved it</Trans>
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function CreateAgentDialog({
  ctx,
  onCreated,
  onClose,
}: {
  ctx: SiteCtx
  onCreated: (e: S['AgentEnrolment']) => void
  onClose: () => void
}) {
  const { t } = useLingui()
  const idem = useIdempotencyKey()
  const [name, setName] = useState('')
  const [error, setError] = useState<string | null>(null)
  const create = useOrgMutation(async () =>
    unwrap(
      gApi.POST('/v1/orgs/{org_id}/agents', {
        params: { path: { org_id: ctx.orgId }, header: idemHeader(idem.key) },
        body: { site_id: ctx.siteId, name: name.trim() },
      }),
    ),
  )
  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : undefined)}>
      <DialogContent>
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault()
            if (!name.trim()) return
            create.mutate(undefined, {
              onSuccess: onCreated,
              onError: () => setError(t`Could not create the agent.`),
            })
          }}
        >
          <DialogHeader>
            <DialogTitle>
              <Trans>New gate agent</Trans>
            </DialogTitle>
            <DialogDescription>
              <Trans>
                The agent runs on a computer at the gate and opens it. You get a one-time token to
                enrol it.
              </Trans>
            </DialogDescription>
          </DialogHeader>
          <Field label={t`Name`} htmlFor="ag-name">
            <Input id="ag-name" value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <FormRootError message={error ?? undefined} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              <Trans>Cancel</Trans>
            </Button>
            <Button type="submit" disabled={!name.trim() || create.isPending}>
              <Trans>Create</Trans>
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

export function AgentFlags({ a }: { a: S['Agent'] }) {
  const { t } = useLingui()
  return (
    <div className="flex flex-wrap gap-1">
      {a.clock_suspect ? <Badge variant="destructive">{t`Clock suspect`}</Badge> : null}
      {a.store_error ? (
        <Badge variant="destructive" title={a.store_error}>
          {t`Store error`}
        </Badge>
      ) : null}
      {(a.lost_events ?? 0) > 0 ? (
        <Badge variant="warning">{t`Lost events: ${a.lost_events}`}</Badge>
      ) : null}
      {(a.dead_letters ?? 0) > 0 ? (
        <Badge variant="warning">{t`Dead letters: ${a.dead_letters}`}</Badge>
      ) : null}
      {a.drivers.map((d) => (
        <Badge
          key={d.instance}
          variant={d.state === 'ok' ? 'secondary' : 'warning'}
          title={d.detail}
        >
          {d.instance}: {d.state}
          {d.mock ? ` (${t`mock`})` : ''}
        </Badge>
      ))}
    </div>
  )
}

/** Gate agents: status, last seen, flags; create, re-enrol, rotate, revoke (D6). */
export function AgentsTab({ ctx, canManage }: { ctx: SiteCtx; canManage: boolean }) {
  const { t } = useLingui()
  const agents = useAgents(ctx)
  const keys = useKeyStore()
  const [creating, setCreating] = useState(false)
  const [enrolment, setEnrolment] = useState<S['AgentEnrolment'] | null>(null)
  const [revoking, setRevoking] = useState<S['Agent'] | null>(null)
  const act = useOrgMutation(
    async (v: { agent: S['Agent']; kind: 'rotate-token' | 'reenrol' | 'revoke' }) => {
      const id = `${v.agent.id}-${v.kind}`
      const params = {
        path: { org_id: ctx.orgId, agent_id: v.agent.id },
        header: idemHeader(keys.get(id)),
      }
      let out: S['AgentEnrolment'] | S['Agent']
      if (v.kind === 'reenrol')
        out = await unwrap(gApi.POST('/v1/orgs/{org_id}/agents/{agent_id}:reenrol', { params }))
      else if (v.kind === 'revoke')
        out = await unwrap(gApi.POST('/v1/orgs/{org_id}/agents/{agent_id}:revoke', { params }))
      else
        out = await unwrap(
          gApi.POST('/v1/orgs/{org_id}/agents/{agent_id}:rotate-token', { params }),
        )
      keys.done(id)
      return out
    },
  )
  const rebuild = useOrgMutation(async () => {
    await gApi.POST('/v1/orgs/{org_id}/access/allowlist:rebuild', {
      params: { path: { org_id: ctx.orgId }, header: idemHeader(keys.get('rebuild')) },
      body: { site_id: ctx.siteId },
    })
    keys.done('rebuild')
  })
  const statusBadge = (a: S['Agent']) =>
    a.status === 'revoked' ? (
      <Badge variant="secondary">{t`Revoked`}</Badge>
    ) : a.status === 'pending' ? (
      <Badge variant="info">{t`Waiting for enrolment`}</Badge>
    ) : a.connected ? (
      <Badge variant="success">{t`Online`}</Badge>
    ) : (
      <Badge variant="destructive">{t`Offline`}</Badge>
    )
  return (
    <div className="flex flex-col gap-4">
      {canManage ? (
        <div className="flex flex-wrap gap-2">
          <Button onClick={() => setCreating(true)}>
            <PlusIcon />
            <Trans>New agent</Trans>
          </Button>
          <Button
            variant="outline"
            disabled={rebuild.isPending}
            onClick={() =>
              rebuild.mutate(undefined, {
                onSuccess: () => toast.info(t`The allow-list is being rebuilt.`),
                onError: () => toast.error(t`Could not start the rebuild.`),
              })
            }
          >
            <Trans>Rebuild the allow-list</Trans>
          </Button>
        </div>
      ) : null}
      <QueryBoundary
        query={agents}
        isEmpty={(d) => d.length === 0}
        emptyTitle={t`No gate agents yet`}
        emptyDescription={canManage ? t`Create an agent to connect a gate or door.` : undefined}
      >
        {(items) => (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>
                  <Trans>Agent</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Status</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Last seen</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Details</Trans>
                </TableHead>
                <TableHead>
                  <span className="sr-only">
                    <Trans>Actions</Trans>
                  </span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((a) => (
                <TableRow key={a.id}>
                  <TableCell>
                    <span className="font-medium">{a.name}</span>
                    <span className="block text-xs text-muted-foreground">{a.platform ?? '–'}</span>
                  </TableCell>
                  <TableCell>{statusBadge(a)}</TableCell>
                  <TableCell>
                    {a.last_seen ? (
                      <SiteDate value={a.last_seen} timeZone={ctx.timeZone} kind="datetime" />
                    ) : (
                      '–'
                    )}
                  </TableCell>
                  <TableCell>
                    <AgentFlags a={a} />
                    {a.queue_depth ? (
                      <span className="text-xs text-muted-foreground">{t`Queue: ${a.queue_depth}`}</span>
                    ) : null}
                  </TableCell>
                  <TableCell>
                    {canManage && a.status !== 'revoked' ? (
                      <div className="flex flex-wrap gap-1">
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() =>
                            act.mutate(
                              { agent: a, kind: 'reenrol' },
                              {
                                onSuccess: (r) => setEnrolment(r as S['AgentEnrolment']),
                                onError: () => toast.error(t`Could not re-enrol the agent.`),
                              },
                            )
                          }
                        >
                          <Trans>Re-enrol</Trans>
                        </Button>
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() =>
                            act.mutate(
                              { agent: a, kind: 'rotate-token' },
                              {
                                onSuccess: () => toast.info(t`Token rotation started.`),
                                onError: () => toast.error(t`Could not rotate the token.`),
                              },
                            )
                          }
                        >
                          <Trans>Rotate token</Trans>
                        </Button>
                        <Button size="sm" variant="ghost" onClick={() => setRevoking(a)}>
                          <Trans>Revoke</Trans>
                        </Button>
                      </div>
                    ) : null}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </QueryBoundary>
      {creating ? (
        <CreateAgentDialog
          ctx={ctx}
          onClose={() => setCreating(false)}
          onCreated={(e) => {
            setCreating(false)
            setEnrolment(e)
          }}
        />
      ) : null}
      {enrolment ? (
        <EnrolTokenDialog ctx={ctx} enrolment={enrolment} onClose={() => setEnrolment(null)} />
      ) : null}
      <ConfirmDialog
        open={revoking !== null}
        onOpenChange={(o) => (!o ? setRevoking(null) : undefined)}
        title={t`Revoke this agent?`}
        description={t`Its gates stop opening from the app and it can no longer connect.`}
        confirmLabel={t`Revoke`}
        destructive
        pending={act.isPending}
        onConfirm={() =>
          revoking &&
          act.mutate(
            { agent: revoking, kind: 'revoke' },
            {
              onSuccess: () => setRevoking(null),
              onError: () => toast.error(t`Could not revoke the agent.`),
            },
          )
        }
      />
    </div>
  )
}
