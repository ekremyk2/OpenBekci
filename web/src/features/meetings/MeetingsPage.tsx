import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import { CalendarClockIcon, PlusIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import { useSession } from '@/app/session'
import { SiteDate } from '@/components/data/SiteDate'
import { Badge } from '@/components/ui/badge'
import { buttonVariants } from '@/components/ui/button'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { cn } from '@/lib/utils'
import { gApi, unwrap, useOrgQuery, useSiteCtx, type SiteCtx } from './kit/api'
import { Callout } from './kit/widgets'
import { PageHeader, QueryBoundary, Select } from './kit/ui'
import { MEETING_STATUS_VARIANT, useGovLabels } from './labels'

/** Term-expiry reminders (G6): memberships ending soon call for an assembly. */
export function TermBanners({ ctx }: { ctx: SiteCtx }) {
  const { can } = useSession()
  const labels = useGovLabels()
  const q = useOrgQuery(
    ['term-reminders', ctx.siteId],
    () =>
      unwrap(
        gApi.GET('/v1/orgs/{org_id}/term-reminders', {
          params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId } },
        }),
      ).then((r) => r.items),
    { enabled: can('meetings.manage') },
  )
  if (!q.data || q.data.length === 0) return null
  return (
    <div className="flex flex-col gap-2" data-testid="term-banners">
      {q.data.map((r) => (
        <Callout key={r.id} tone={r.status === 'upcoming' ? 'info' : 'warning'} role="status">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span>
              <Trans>
                The term of {r.person_display_name} as {labels.role(r.role)} ends on{' '}
                <SiteDate value={r.valid_to} timeZone={ctx.timeZone} /> ({r.days_left} days left).
                Schedule an assembly in time.
              </Trans>
            </span>
            <Link
              to="/orgs/$orgId/meetings/new"
              params={{ orgId: ctx.orgId }}
              className={buttonVariants({ size: 'sm', variant: 'outline' })}
            >
              <Trans>Plan an assembly</Trans>
            </Link>
          </div>
        </Callout>
      ))}
    </div>
  )
}

export function MeetingsPage() {
  const { t } = useLingui()
  const { can } = useSession()
  const ctx = useSiteCtx()!
  const labels = useGovLabels()
  const manage = can('meetings.manage')
  const [status, setStatus] = useState('')
  const q = useOrgQuery(['meetings', ctx.siteId, status], () =>
    unwrap(
      gApi.GET('/v1/orgs/{org_id}/meetings', {
        params: {
          path: { org_id: ctx.orgId },
          query: { site_id: ctx.siteId, limit: 100, ...(status ? { status } : {}) },
        },
      }),
    ).then((r) => r.items),
  )
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={t`Assemblies`}
        description={t`Calls, attendance, decisions and minutes of the owners' assembly.`}
        actions={
          manage ? (
            <Link
              to="/orgs/$orgId/meetings/new"
              params={{ orgId: ctx.orgId }}
              className={buttonVariants()}
            >
              <PlusIcon />
              <Trans>New assembly</Trans>
            </Link>
          ) : null
        }
      />
      <TermBanners ctx={ctx} />
      <div className="flex gap-2">
        <Select
          aria-label={t`Status`}
          className="w-auto"
          value={status}
          onChange={(e) => setStatus(e.target.value)}
        >
          <option value="">{t`All statuses`}</option>
          {['draft', 'called', 'in_session', 'closed', 'cancelled'].map((s) => (
            <option key={s} value={s}>
              {labels.meetingStatus(s)}
            </option>
          ))}
        </Select>
      </div>
      <QueryBoundary
        query={q}
        isEmpty={(d) => d.length === 0}
        emptyTitle={t`No assemblies yet`}
        emptyDescription={
          manage ? t`Create the first assembly to start the call.` : t`Nothing has been scheduled.`
        }
      >
        {(items) => (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>
                  <Trans>Assembly</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Date</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Place</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Status</Trans>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((m) => (
                <TableRow key={m.id}>
                  <TableCell>
                    <Link
                      to="/orgs/$orgId/meetings/$meetingId"
                      params={{ orgId: ctx.orgId, meetingId: m.id }}
                      className={cn('font-medium underline-offset-4 hover:underline')}
                    >
                      {m.title ?? labels.meetingKind(m.kind)}
                    </Link>
                    <div className="text-xs text-muted-foreground">
                      {labels.meetingKind(m.kind)}
                    </div>
                  </TableCell>
                  <TableCell>
                    <CalendarClockIcon
                      className="me-1 inline size-4 text-muted-foreground"
                      aria-hidden="true"
                    />
                    <SiteDate value={m.scheduled_at} timeZone={ctx.timeZone} kind="datetime" />
                  </TableCell>
                  <TableCell>{m.location}</TableCell>
                  <TableCell>
                    <Badge variant={MEETING_STATUS_VARIANT[m.status] ?? 'secondary'}>
                      {labels.meetingStatus(m.status)}
                    </Badge>
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
