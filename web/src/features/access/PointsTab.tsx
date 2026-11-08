import { useMemo, useState } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Trans, useLingui } from '@lingui/react/macro'
import { DoorOpenIcon, PlusIcon } from 'lucide-react'
import { AppError } from '@/api/errors'
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
import { agentOnline, manualOpenState } from '@/features/gate/openState'
import {
  gApi,
  idemHeader,
  ifMatch,
  unwrap,
  useOrgMutation,
  useOrgQuery,
  type S,
  type SiteCtx,
} from '@/features/meetings/kit/api'
import {
  applyServerErrors,
  LabeledCheckbox,
  ConfirmDialog,
  Field,
  FormRootError,
  QueryBoundary,
  Select,
} from '@/features/meetings/kit/ui'
import { useAgents } from './AgentsTab'

function usePoints(ctx: SiteCtx) {
  return useOrgQuery(['points', ctx.siteId], () =>
    unwrap(
      gApi.GET('/v1/orgs/{org_id}/access-points', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, limit: 100 } },
      }),
    ).then((r) => r.items),
  )
}

function PointDialog({
  ctx,
  point,
  agents,
  onClose,
}: {
  ctx: SiteCtx
  point: S['AccessPoint'] | null
  agents: S['Agent'][]
  onClose: () => void
}) {
  const { t } = useLingui()
  const idem = useIdempotencyKey()
  const schema = useMemo(
    () =>
      z.object({
        name: z
          .string()
          .trim()
          .min(1, t`Enter a name.`),
        kind: z.enum(['vehicle_gate', 'pedestrian_door']),
        direction: z.enum(['in', 'out', 'both']),
        agent_id: z.string(),
        instance: z.string().trim(),
        channel: z.string().trim(),
        enabled: z.boolean(),
      }),
    [t],
  )
  type Values = z.infer<typeof schema>
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: {
      name: point?.name ?? '',
      kind: point?.kind ?? 'vehicle_gate',
      direction: point?.direction ?? 'both',
      agent_id: point?.agent_id ?? '',
      instance: point?.driver_ref?.instance ?? '',
      channel: point?.driver_ref?.channel ?? '',
      enabled: point?.enabled ?? true,
    },
  })
  const save = useOrgMutation(async (v: Values) => {
    const body = {
      name: v.name.trim(),
      kind: v.kind,
      direction: v.direction,
      agent_id: v.agent_id === '' ? null : v.agent_id,
      driver_ref:
        v.instance === ''
          ? null
          : { instance: v.instance, ...(v.channel ? { channel: v.channel } : {}) },
      enabled: v.enabled,
    }
    if (point)
      return unwrap(
        gApi.PATCH('/v1/orgs/{org_id}/access-points/{point_id}', {
          params: {
            path: { org_id: ctx.orgId, point_id: point.id },
            header: ifMatch(point.version),
          },
          body,
        }),
      )
    return unwrap(
      gApi.POST('/v1/orgs/{org_id}/access-points', {
        params: { path: { org_id: ctx.orgId }, header: idemHeader(idem.key) },
        body: { site_id: ctx.siteId, ...body },
      }),
    )
  })
  const err = form.formState.errors
  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : undefined)}>
      <DialogContent>
        <form
          className="flex flex-col gap-4"
          noValidate
          onSubmit={form.handleSubmit(async (v) => {
            try {
              await save.mutateAsync(v)
              onClose()
            } catch (e) {
              applyServerErrors(form, e, t`Could not save the access point.`)
            }
          })}
        >
          <DialogHeader>
            <DialogTitle>{point ? t`Edit access point` : t`New access point`}</DialogTitle>
            <DialogDescription>
              <Trans>
                A gate or door an agent can open. Driver details are configured on the agent
                computer.
              </Trans>
            </DialogDescription>
          </DialogHeader>
          <Field label={t`Name`} htmlFor="ap-name" error={err.name?.message}>
            <Input id="ap-name" {...form.register('name')} />
          </Field>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label={t`Kind`} htmlFor="ap-kind">
              <Select id="ap-kind" {...form.register('kind')}>
                <option value="vehicle_gate">{t`Vehicle gate`}</option>
                <option value="pedestrian_door">{t`Pedestrian door`}</option>
              </Select>
            </Field>
            <Field label={t`Direction`} htmlFor="ap-dir">
              <Select id="ap-dir" {...form.register('direction')}>
                <option value="both">{t`In and out`}</option>
                <option value="in">{t`In`}</option>
                <option value="out">{t`Out`}</option>
              </Select>
            </Field>
          </div>
          <Field label={t`Agent`} htmlFor="ap-agent">
            <Select id="ap-agent" {...form.register('agent_id')}>
              <option value="">{t`No agent`}</option>
              {agents
                .filter((a) => a.status !== 'revoked')
                .map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name}
                  </option>
                ))}
            </Select>
          </Field>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label={t`Driver instance`} htmlFor="ap-inst">
              <Input id="ap-inst" dir="ltr" {...form.register('instance')} />
            </Field>
            <Field label={t`Channel`} htmlFor="ap-chan">
              <Input id="ap-chan" dir="ltr" {...form.register('channel')} />
            </Field>
          </div>
          <LabeledCheckbox label={t`Enabled`} {...form.register('enabled')} />
          <FormRootError message={err.root?.message} />
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

/** Manual open from the admin screen (the guard console has its own); explains why it is disabled. */
function AdminOpenButton({
  ctx,
  point,
  agent,
  canOpen,
}: {
  ctx: SiteCtx
  point: S['AccessPoint']
  agent: S['Agent'] | undefined
  canOpen: boolean
}) {
  const { t } = useLingui()
  const k = useIdempotencyKey()
  const state = manualOpenState({
    canOpen,
    point,
    agent: agent ? { online: agentOnline(agent), clock_suspect: agent.clock_suspect } : null,
  })
  const open = useOrgMutation(
    async () =>
      unwrap(
        gApi.POST('/v1/orgs/{org_id}/access-points/{point_id}:open', {
          params: { path: { org_id: ctx.orgId, point_id: point.id }, header: idemHeader(k.key) },
          body: {},
        }),
      ),
    { invalidate: false },
  )
  const why = {
    no_permission: t`You may not open gates.`,
    point_disabled: t`This point is switched off.`,
    agent_offline: t`The agent is offline.`,
    point_offline: t`The agent cannot reach this point.`,
    clock_suspect: t`The agent's clock is wrong.`,
  }
  return (
    <div className="flex flex-col gap-1">
      <Button
        size="sm"
        variant="outline"
        disabled={!state.enabled || open.isPending}
        onClick={() =>
          open.mutate(undefined, {
            onSuccess: () => {
              k.renew()
              toast.info(t`Open command sent.`)
            },
            onError: (e) =>
              toast.error(
                e instanceof AppError && e.code === 'agent_offline'
                  ? t`The agent is offline.`
                  : t`Could not open.`,
              ),
          })
        }
      >
        <DoorOpenIcon />
        <Trans>Open</Trans>
      </Button>
      {state.reason ? (
        <span className="text-xs text-muted-foreground">{why[state.reason]}</span>
      ) : null}
    </div>
  )
}

/** Access points CRUD (D6). */
export function PointsTab({
  ctx,
  canManage,
  canOpen,
}: {
  ctx: SiteCtx
  canManage: boolean
  canOpen: boolean
}) {
  const { t } = useLingui()
  const points = usePoints(ctx)
  const agents = useAgents(ctx)
  const [editing, setEditing] = useState<S['AccessPoint'] | 'new' | null>(null)
  const [deleting, setDeleting] = useState<S['AccessPoint'] | null>(null)
  const remove = useOrgMutation(async (p: S['AccessPoint']) => {
    await gApi.DELETE('/v1/orgs/{org_id}/access-points/{point_id}', {
      params: { path: { org_id: ctx.orgId, point_id: p.id }, header: ifMatch(p.version) },
    })
  })
  const agentName = (id: string | null) => agents.data?.find((a) => a.id === id)?.name ?? '–'
  return (
    <div className="flex flex-col gap-4">
      {canManage ? (
        <div>
          <Button onClick={() => setEditing('new')}>
            <PlusIcon />
            <Trans>New access point</Trans>
          </Button>
        </div>
      ) : null}
      <QueryBoundary
        query={points}
        isEmpty={(d) => d.length === 0}
        emptyTitle={t`No access points yet`}
        emptyDescription={canManage ? t`Add the gates and doors of the site.` : undefined}
      >
        {(items) => (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>
                  <Trans>Name</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Kind</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Agent</Trans>
                </TableHead>
                <TableHead>
                  <Trans>State</Trans>
                </TableHead>
                <TableHead>
                  <span className="sr-only">
                    <Trans>Actions</Trans>
                  </span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((p) => (
                <TableRow key={p.id}>
                  <TableCell className="font-medium">{p.name}</TableCell>
                  <TableCell>
                    {p.kind === 'vehicle_gate' ? t`Vehicle gate` : t`Pedestrian door`}
                  </TableCell>
                  <TableCell>{agentName(p.agent_id)}</TableCell>
                  <TableCell>
                    <Badge variant={p.enabled ? 'success' : 'secondary'}>
                      {p.enabled ? t`Enabled` : t`Disabled`}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    <div className="flex flex-wrap items-start gap-2">
                      <AdminOpenButton
                        ctx={ctx}
                        point={p}
                        agent={agents.data?.find((a) => a.id === p.agent_id)}
                        canOpen={canOpen}
                      />
                      {canManage ? (
                        <>
                          <Button size="sm" variant="outline" onClick={() => setEditing(p)}>
                            <Trans>Edit</Trans>
                          </Button>
                          <Button size="sm" variant="ghost" onClick={() => setDeleting(p)}>
                            <Trans>Delete</Trans>
                          </Button>
                        </>
                      ) : null}
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </QueryBoundary>
      {editing ? (
        <PointDialog
          ctx={ctx}
          point={editing === 'new' ? null : editing}
          agents={agents.data ?? []}
          onClose={() => setEditing(null)}
        />
      ) : null}
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(o) => (!o ? setDeleting(null) : undefined)}
        title={t`Delete this access point?`}
        confirmLabel={t`Delete`}
        destructive
        pending={remove.isPending}
        onConfirm={() =>
          deleting && remove.mutate(deleting, { onSuccess: () => setDeleting(null) })
        }
      />
    </div>
  )
}
