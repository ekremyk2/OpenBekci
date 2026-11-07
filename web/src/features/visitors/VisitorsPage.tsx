import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import { CarIcon, PlusIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import { useSession } from '@/app/session'
import { SiteDate } from '@/components/data/SiteDate'
import { Badge } from '@/components/ui/badge'
import { Button, buttonVariants } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { toast } from '@/lib/toast'
import { gApi, idemHeader, unwrap, useKeyStore, useOrgMutation, useOrgQuery, useSiteCtx, type S, type SiteCtx } from '@/features/meetings/kit/api'
import { PageHeader, QueryBoundary, Select } from '@/features/meetings/kit/ui'
import { ApprovalBadge, ApprovalInbox } from './ApprovalInbox'
import { PreRegisterDialog } from './PreRegisterDialog'
import { RecurringTab } from './RecurringTab'

export function entryStatus(e: Pick<S['VisitorEntry'], 'cancelled' | 'arrived_at' | 'left_at' | 'approval_status'>) {
  if (e.cancelled) return 'cancelled' as const
  if (e.left_at) return 'left' as const
  if (e.arrived_at) return 'arrived' as const
  return e.approval_status
}

function StatusBadge({ status }: { status: ReturnType<typeof entryStatus> }) {
  const { t } = useLingui()
  const map = {
    cancelled: [t`Cancelled`, 'secondary'],
    left: [t`Left`, 'secondary'],
    arrived: [t`On site`, 'success'],
    pre_approved: [t`Expected`, 'info'],
    requested: [t`Waiting for approval`, 'warning'],
    approved: [t`Approved`, 'success'],
    denied: [t`Denied`, 'destructive'],
    expired: [t`Expired`, 'secondary'],
  } as const
  const [label, variant] = map[status] ?? [status, 'secondary']
  return <Badge variant={variant}>{label}</Badge>
}

function HistoryTab({ ctx, canManage }: { ctx: SiteCtx; canManage: boolean }) {
  const { t } = useLingui()
  const keys = useKeyStore()
  const [q, setQ] = useState('')
  const [status, setStatus] = useState('')
  const list = useOrgQuery(['visitor-entries', ctx.siteId, q, status], () =>
    unwrap(
      gApi.GET('/v1/orgs/{org_id}/visitor-entries', {
        params: {
          path: { org_id: ctx.orgId },
          query: { site_id: ctx.siteId, limit: 100, ...(q.trim() ? { q: q.trim() } : {}), ...(status ? { approval_status: status } : {}) },
        },
      }),
    ).then((r) => r.items),
    { refetchInterval: 15_000 },
  )
  const cancel = useOrgMutation(async (e: S['VisitorEntry']) => {
    await unwrap(
      gApi.POST('/v1/orgs/{org_id}/visitor-entries/{entry_id}:cancel', {
        params: { path: { org_id: ctx.orgId, entry_id: e.id }, header: idemHeader(keys.get(e.id)) },
      }),
    )
    keys.done(e.id)
  })
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap gap-2">
        <Input aria-label={t`Search visitors`} placeholder={t`Search name or plate`} className="max-w-xs" value={q} onChange={(e) => setQ(e.target.value)} />
        <Select aria-label={t`Status`} className="w-auto" value={status} onChange={(e) => setStatus(e.target.value)}>
          <option value="">{t`Any status`}</option>
          <option value="pre_approved">{t`Expected`}</option>
          <option value="approved">{t`Approved`}</option>
          <option value="denied">{t`Denied`}</option>
          <option value="expired">{t`Expired`}</option>
        </Select>
      </div>
      <QueryBoundary
        query={list}
        isEmpty={(d) => d.length === 0}
        emptyTitle={t`No visitors yet`}
        emptyDescription={canManage ? t`Pre-register a guest to get a QR code for the gate.` : undefined}
      >
        {(items) => (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>
                  <Trans>Visitor</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Unit</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Window</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Status</Trans>
                </TableHead>
                <TableHead>
                  <span className="sr-only">
                    <Trans>Actions</Trans>
                  </span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((e) => (
                <TableRow key={e.id}>
                  <TableCell>
                    <span className="font-medium">{e.name}</span>
                    {e.plate ? (
                      <span className="block text-xs text-muted-foreground" dir="ltr">
                        {e.plate}
                      </span>
                    ) : null}
                  </TableCell>
                  <TableCell>{e.unit_door_number}</TableCell>
                  <TableCell className="text-sm">
                    <SiteDate value={e.window_from} timeZone={ctx.timeZone} kind="datetime" />
                    {' – '}
                    <SiteDate value={e.window_to} timeZone={ctx.timeZone} kind="time" />
                  </TableCell>
                  <TableCell>
                    <StatusBadge status={entryStatus(e)} />
                  </TableCell>
                  <TableCell>
                    {canManage && !e.cancelled && !e.arrived_at ? (
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={cancel.isPending}
                        onClick={() => cancel.mutate(e, { onSuccess: () => toast.info(t`The pass was cancelled.`) })}
                      >
                        <Trans>Cancel pass</Trans>
                      </Button>
                    ) : null}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </QueryBoundary>
    </div>
  )
}

/** Visitors (C2): pre-registration, history, recurring visitors, approval requests. */
export function VisitorsPage() {
  const { t } = useLingui()
  const { can } = useSession()
  const ctx = useSiteCtx()!
  const canManage = can('visitors.manage')
  const canDecide = can('visitors.decide')
  const [adding, setAdding] = useState(false)
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={t`Visitors`}
        description={t`Pre-register guests, share their QR code, and answer the guard's requests.`}
        actions={
          <>
            <Link
              to={'/orgs/$orgId/vehicles' as '/orgs/$orgId'}
              params={{ orgId: ctx.orgId }}
              className={buttonVariants({ variant: 'outline' })}
            >
              <CarIcon />
              <Trans>My vehicles</Trans>
            </Link>
            {canManage ? (
              <Button onClick={() => setAdding(true)}>
                <PlusIcon />
                <Trans>Pre-register a visitor</Trans>
              </Button>
            ) : null}
          </>
        }
      />
      <Tabs defaultValue={canDecide ? 'approvals' : 'history'}>
        <TabsList className="h-auto flex-wrap">
          <TabsTrigger value="history">{t`Visitors`}</TabsTrigger>
          <TabsTrigger value="recurring">{t`Recurring`}</TabsTrigger>
          {canDecide ? (
            <TabsTrigger value="approvals" className="gap-2">
              {t`Approvals`}
              <ApprovalBadge ctx={ctx} enabled={canDecide} />
            </TabsTrigger>
          ) : null}
        </TabsList>
        <TabsContent value="history">
          <HistoryTab ctx={ctx} canManage={canManage} />
        </TabsContent>
        <TabsContent value="recurring">
          <RecurringTab ctx={ctx} canManage={canManage} />
        </TabsContent>
        {canDecide ? (
          <TabsContent value="approvals">
            <ApprovalInbox ctx={ctx} />
          </TabsContent>
        ) : null}
      </Tabs>
      {adding ? <PreRegisterDialog ctx={ctx} onClose={() => setAdding(false)} /> : null}
    </div>
  )
}
