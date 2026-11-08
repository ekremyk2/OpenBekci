import { useState } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import { SiteDate } from '@/components/data/SiteDate'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { gApi, unwrap, useOrgQuery, type SiteCtx } from '@/features/meetings/kit/api'
import { instantToLocalInput, localInputToInstant } from '@/features/meetings/kit/time'
import { QueryBoundary, Select } from '@/features/meetings/kit/ui'

const REASONS = [
  'match',
  'unknown',
  'outside_window',
  'wrong_point',
  'revoked',
  'used_up',
  'agent_stopped',
  'guard',
  'manager',
  'remote',
  'driver_error',
]

/** Opening log with filters: point, decision, reason, plate, period (D6). */
export function LogTab({ ctx }: { ctx: SiteCtx }) {
  const { t } = useLingui()
  const [point, setPoint] = useState('')
  const [decision, setDecision] = useState('')
  const [reason, setReason] = useState('')
  const [plate, setPlate] = useState('')
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  // fixed when the tab opens: a render must not read the clock
  const [maxTo] = useState(() =>
    instantToLocalInput(new Date(Date.now() + 86_400_000).toISOString(), ctx.timeZone),
  )
  const points = useOrgQuery(['points', ctx.siteId], () =>
    unwrap(
      gApi.GET('/v1/orgs/{org_id}/access-points', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, limit: 100 } },
      }),
    ).then((r) => r.items),
  )
  const events = useOrgQuery(
    ['access-events', ctx.siteId, point, decision, reason, plate, from, to],
    () =>
      unwrap(
        gApi.GET('/v1/orgs/{org_id}/access/events', {
          params: {
            path: { org_id: ctx.orgId },
            query: {
              site_id: ctx.siteId,
              limit: 100,
              ...(point ? { point_id: point } : {}),
              ...(decision ? { decision } : {}),
              ...(reason ? { reason } : {}),
              ...(plate.trim() ? { plate: plate.trim() } : {}),
              ...(from ? { from: localInputToInstant(from, ctx.timeZone) } : {}),
              ...(to ? { to: localInputToInstant(to, ctx.timeZone) } : {}),
            },
          },
        }),
      ).then((r) => r.items),
    { refetchInterval: 30_000 },
  )
  const decisions = { allow: t`Allowed`, deny: t`Refused`, manual_open: t`Opened by hand` }
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-end gap-2">
        <Select
          aria-label={t`Access point`}
          className="w-auto"
          value={point}
          onChange={(e) => setPoint(e.target.value)}
        >
          <option value="">{t`All points`}</option>
          {(points.data ?? []).map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </Select>
        <Select
          aria-label={t`Decision`}
          className="w-auto"
          value={decision}
          onChange={(e) => setDecision(e.target.value)}
        >
          <option value="">{t`Any decision`}</option>
          {Object.entries(decisions).map(([k, v]) => (
            <option key={k} value={k}>
              {v}
            </option>
          ))}
        </Select>
        <Select
          aria-label={t`Reason`}
          className="w-auto"
          value={reason}
          onChange={(e) => setReason(e.target.value)}
        >
          <option value="">{t`Any reason`}</option>
          {REASONS.map((r) => (
            <option key={r} value={r}>
              {r.replaceAll('_', ' ')}
            </option>
          ))}
        </Select>
        <Input
          aria-label={t`Plate`}
          placeholder={t`Plate`}
          dir="ltr"
          className="w-36"
          value={plate}
          onChange={(e) => setPlate(e.target.value)}
        />
        <Input
          aria-label={t`From`}
          type="datetime-local"
          dir="ltr"
          className="w-auto"
          value={from}
          onChange={(e) => setFrom(e.target.value)}
        />
        <Input
          aria-label={t`Until`}
          type="datetime-local"
          dir="ltr"
          className="w-auto"
          value={to}
          onChange={(e) => setTo(e.target.value)}
          max={maxTo}
        />
      </div>
      <QueryBoundary
        query={events}
        isEmpty={(d) => d.length === 0}
        emptyTitle={t`No openings match`}
      >
        {(items) => (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>
                  <Trans>Time</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Point</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Decision</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Reason</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Plate</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Unit</Trans>
                </TableHead>
                <TableHead>
                  <Trans>By</Trans>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((e) => (
                <TableRow key={e.id}>
                  <TableCell>
                    <SiteDate value={e.occurred_at} timeZone={ctx.timeZone} kind="datetime" />
                  </TableCell>
                  <TableCell>{e.point_name ?? e.point_id}</TableCell>
                  <TableCell>
                    <Badge variant={e.decision === 'deny' ? 'destructive' : 'success'}>
                      {decisions[e.decision] ?? e.decision}
                    </Badge>
                  </TableCell>
                  <TableCell>{e.reason.replaceAll('_', ' ')}</TableCell>
                  <TableCell dir="ltr">{e.observed_plate ?? '–'}</TableCell>
                  <TableCell>{e.unit_door_number ?? '–'}</TableCell>
                  <TableCell>{e.actor_display_name ?? '–'}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </QueryBoundary>
    </div>
  )
}
