import { useMemo, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { ArrowLeftIcon, CheckIcon, UndoIcon, UsersIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import { AppError } from '@/api/errors'
import { Badge } from '@/components/ui/badge'
import { Button, buttonVariants } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { toast } from '@/lib/toast'
import {
  gApi,
  idemHeader,
  unwrap,
  useKeyStore,
  useOrgMutation,
  useOrgQuery,
  useSiteCtx,
} from './kit/api'
import { QrScanner } from './kit/QrScanner'
import { Field, PageHeader, QueryBoundary, Select } from './kit/ui'
import { Callout, DownloadButton } from './kit/widgets'
import { QuorumCard } from './QuorumCard'
import { useGovernanceSettings, useMeeting, useOwnerRows, usePersons } from './hooks'
import { proxyLimit, proxyWarnings, type OwnerRow } from './logic'

const POLL_MS = 5000

/** Warnings text for the proxy form; exported for the tests. */
export function ProxyWarnings({
  warnings,
  limit,
}: {
  warnings: ('proxy_cap' | 'max_votes')[]
  limit: number
}) {
  if (warnings.length === 0) return null
  return (
    <Callout tone="warning" role="alert">
      <ul className="list-disc ps-5">
        {warnings.includes('proxy_cap') ? (
          <li>
            <Trans>
              This person would hold more proxies than the limit of {limit} for this site.
            </Trans>
          </li>
        ) : null}
        {warnings.includes('max_votes') ? (
          <li>
            <Trans>This person would hold more than the maximum share of all votes.</Trans>
          </li>
        ) : null}
      </ul>
    </Callout>
  )
}

/**
 * The attendance desk (G4): sized for a tablet at the door. Tick owners present or by proxy, scan
 * the QR of an owner, watch the quorum by number and by land share, print the hazirun cetveli.
 */
export function AttendanceDeskPage({ meetingId }: { meetingId: string }) {
  const { t } = useLingui()
  const ctx = useSiteCtx()!
  const keys = useKeyStore()
  const meeting = useMeeting(ctx.orgId, meetingId)
  const owners = useOwnerRows(ctx.orgId, ctx.siteId)
  const persons = usePersons(ctx.orgId)
  const settings = useGovernanceSettings(ctx.orgId, ctx.siteId)
  const open = meeting.data?.status === 'called' || meeting.data?.status === 'in_session'
  const quorum = useOrgQuery(
    ['quorum', meetingId],
    () =>
      unwrap(
        gApi.GET('/v1/orgs/{org_id}/meetings/{meeting_id}/quorum', {
          params: { path: { org_id: ctx.orgId, meeting_id: meetingId } },
        }),
      ),
    { refetchInterval: POLL_MS },
  )
  const attendance = useOrgQuery(
    ['attendance', meetingId],
    () =>
      unwrap(
        gApi.GET('/v1/orgs/{org_id}/meetings/{meeting_id}/attendance', {
          params: { path: { org_id: ctx.orgId, meeting_id: meetingId }, query: { limit: 200 } },
        }),
      ).then((r) => r.items),
    { refetchInterval: POLL_MS },
  )
  const [search, setSearch] = useState('')
  const [proxyFor, setProxyFor] = useState<string | null>(null)

  const record = useOrgMutation(
    async (v: {
      id: string
      person_id: string
      unit_ids: string[]
      mode: 'in_person' | 'proxy'
      proxy_for?: string
    }) => {
      const r = await unwrap(
        gApi.POST('/v1/orgs/{org_id}/meetings/{meeting_id}/attendance', {
          params: {
            path: { org_id: ctx.orgId, meeting_id: meetingId },
            header: idemHeader(keys.get(v.id)),
          },
          body: {
            person_id: v.person_id,
            unit_ids: v.unit_ids,
            mode: v.mode,
            ...(v.proxy_for ? { proxy_for_person_id: v.proxy_for } : {}),
          },
        }),
      )
      keys.done(v.id)
      return r
    },
  )
  const remove = useOrgMutation(async (attendanceId: string) => {
    await gApi.DELETE('/v1/orgs/{org_id}/meetings/{meeting_id}/attendance/{attendance_id}', {
      params: { path: { org_id: ctx.orgId, meeting_id: meetingId, attendance_id: attendanceId } },
    })
  })
  const checkin = useOrgMutation(async (token: string) => {
    return unwrap(
      gApi.POST('/v1/orgs/{org_id}/meetings/{meeting_id}/attendance:checkin', {
        params: {
          path: { org_id: ctx.orgId, meeting_id: meetingId },
          header: idemHeader(keys.get(`qr-${token}`)),
        },
        body: { token },
      }),
    )
  })

  const rows = useMemo(() => {
    const needle = search.trim().toLowerCase()
    return owners.rows.filter(
      (o) =>
        !needle ||
        o.name.toLowerCase().includes(needle) ||
        o.units.some((u) => u.label.toLowerCase().includes(needle)),
    )
  }, [owners.rows, search])

  const att = attendance.data ?? []
  const totalUnits = owners.rows.reduce((a, o) => a + o.units.length, 0)
  const statusOf = (o: OwnerRow) =>
    att.find((a) => a.unit_ids.some((u) => o.units.some((x) => x.id === u)))

  async function onScan(token: string) {
    try {
      const a = await checkin.mutateAsync(token)
      keys.done(`qr-${token}`)
      toast.info(t`${a.person_display_name} checked in.`)
    } catch (e) {
      keys.done(`qr-${token}`)
      toast.error(
        e instanceof AppError && e.status === 409
          ? t`That owner is already checked in.`
          : t`This code is not valid for this assembly.`,
      )
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={t`Attendance desk`}
        description={meeting.data?.title ?? undefined}
        actions={
          <>
            <Link
              to="/orgs/$orgId/meetings/$meetingId"
              params={{ orgId: ctx.orgId, meetingId }}
              className={buttonVariants({ variant: 'outline', size: 'sm' })}
            >
              <ArrowLeftIcon className="rtl-flip" />
              <Trans>Back to the assembly</Trans>
            </Link>
            <DownloadButton
              request={{
                endpoint: '/v1/orgs/{org_id}/meetings/{meeting_id}/attendance-sheet',
                path: { org_id: ctx.orgId, meeting_id: meetingId },
              }}
              filename={`hazirun-cetveli-${meetingId}.pdf`}
            >
              <Trans>Attendance list (PDF)</Trans>
            </DownloadButton>
          </>
        }
      />
      {meeting.data && !open ? (
        <Callout tone="warning" role="status">
          <Trans>Attendance can be recorded only while the assembly is called or in session.</Trans>
        </Callout>
      ) : null}
      <QueryBoundary query={quorum} skeleton="page">
        {(q) => <QuorumCard quorum={q} className="sticky top-0 z-10" />}
      </QueryBoundary>
      <div className="grid gap-4 lg:grid-cols-[1fr_20rem]">
        <Card>
          <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-2">
            <CardTitle className="flex items-center gap-2">
              <UsersIcon className="size-5" aria-hidden="true" />
              <Trans>Owners</Trans>
            </CardTitle>
            <Input
              aria-label={t`Search owners or units`}
              placeholder={t`Search owner or unit`}
              className="h-11 max-w-xs"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </CardHeader>
          <CardContent>
            <QueryBoundary
              query={owners}
              isEmpty={() => owners.rows.length === 0}
              emptyTitle={t`No owners found`}
              emptyDescription={t`Owners come from the occupancies of the site.`}
            >
              {() => (
                <ul className="flex flex-col divide-y" aria-label={t`Owners`}>
                  {rows.map((o) => {
                    const present = statusOf(o)
                    const holder = present?.mode === 'proxy' ? present.person_display_name : null
                    return (
                      <li key={o.personId} className="flex flex-col gap-2 py-3">
                        <div className="flex flex-wrap items-center gap-3">
                          <div className="flex min-w-0 flex-1 flex-col">
                            <span className="truncate text-base font-medium" title={o.name}>
                              {o.name}
                            </span>
                            <span className="truncate text-sm text-muted-foreground">
                              {o.units.map((u) => u.label).join(', ')}
                              {o.sharePct !== null ? ` · ${o.sharePct}%` : ''}
                            </span>
                          </div>
                          {present ? (
                            <Badge variant="success">
                              {holder ? t`By proxy: ${holder}` : t`Present`}
                              {present.source === 'qr' ? ` · ${t`QR`}` : ''}
                            </Badge>
                          ) : (
                            <Badge variant="secondary">
                              <Trans>Absent</Trans>
                            </Badge>
                          )}
                          {present ? (
                            <Button
                              size="lg"
                              variant="outline"
                              disabled={!open || remove.isPending}
                              onClick={() => remove.mutate(present.id)}
                              aria-label={t`Undo attendance of ${o.name}`}
                            >
                              <UndoIcon />
                              <Trans>Undo</Trans>
                            </Button>
                          ) : (
                            <>
                              <Button
                                size="lg"
                                variant="ghost"
                                disabled={!open || record.isPending}
                                onClick={() =>
                                  record.mutate(
                                    {
                                      id: `present-${o.personId}`,
                                      person_id: o.personId,
                                      unit_ids: o.units.map((u) => u.id),
                                      mode: 'in_person',
                                    },
                                    {
                                      onError: () =>
                                        toast.error(t`Could not record the attendance.`),
                                    },
                                  )
                                }
                                aria-label={t`Mark ${o.name} present`}
                              >
                                <CheckIcon />
                                <Trans>Present</Trans>
                              </Button>
                              <Button
                                size="lg"
                                variant="secondary"
                                disabled={!open}
                                aria-expanded={proxyFor === o.personId}
                                onClick={() =>
                                  setProxyFor(proxyFor === o.personId ? null : o.personId)
                                }
                                aria-label={t`Record a proxy for ${o.name}`}
                              >
                                <Trans>Proxy</Trans>
                              </Button>
                            </>
                          )}
                        </div>
                        {proxyFor === o.personId && !present ? (
                          <ProxyForm
                            owner={o}
                            owners={owners.rows}
                            persons={persons.data ?? []}
                            attendance={att}
                            totalUnits={totalUnits}
                            settings={settings.data?.settings}
                            pending={record.isPending}
                            onSubmit={(holderId) =>
                              record.mutate(
                                {
                                  id: `proxy-${o.personId}-${holderId}`,
                                  person_id: holderId,
                                  unit_ids: o.units.map((u) => u.id),
                                  mode: 'proxy',
                                  proxy_for: o.personId,
                                },
                                {
                                  onSuccess: () => setProxyFor(null),
                                  onError: () => toast.error(t`Could not record the proxy.`),
                                },
                              )
                            }
                          />
                        ) : null}
                      </li>
                    )
                  })}
                </ul>
              )}
            </QueryBoundary>
          </CardContent>
        </Card>
        <div className="flex flex-col gap-4">
          <Card>
            <CardHeader>
              <CardTitle>
                <Trans>QR check-in</Trans>
              </CardTitle>
            </CardHeader>
            <CardContent>
              <p className="mb-3 text-sm text-muted-foreground">
                <Trans>Scan the code from the owner's phone or from the printed call letter.</Trans>
              </p>
              <QrScanner
                onScan={(tok) => void onScan(tok)}
                busy={!open || checkin.isPending}
                large
              />
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle>
                <Trans>Checked in</Trans> ({att.length})
              </CardTitle>
            </CardHeader>
            <CardContent>
              {att.length === 0 ? (
                <p className="text-sm text-muted-foreground">
                  <Trans>Nobody yet.</Trans>
                </p>
              ) : (
                <ul className="flex flex-col gap-1 text-sm">
                  {[...att]
                    .sort((a, b) => b.checked_in_at.localeCompare(a.checked_in_at))
                    .map((a) => (
                      <li key={a.id} className="flex justify-between gap-2">
                        <span>{a.person_display_name}</span>
                        <span className="text-muted-foreground">{a.unit_labels.join(', ')}</span>
                      </li>
                    ))}
                </ul>
              )}
            </CardContent>
          </Card>
        </div>
      </div>
    </div>
  )
}

function ProxyForm({
  owner,
  owners,
  persons,
  attendance,
  totalUnits,
  settings,
  pending,
  onSubmit,
}: {
  owner: OwnerRow
  owners: OwnerRow[]
  persons: { id: string; display_name: string }[]
  attendance: { person_id: string; mode: 'in_person' | 'proxy'; unit_ids: string[] }[]
  totalUnits: number
  settings: Parameters<typeof proxyWarnings>[0]['settings'] | undefined
  pending: boolean
  onSubmit: (holderId: string) => void
}) {
  const { t } = useLingui()
  const [holder, setHolder] = useState('')
  const own = owners.find((o) => o.personId === holder)?.units.length ?? 0
  const heldProxies = attendance
    .filter((a) => a.person_id === holder && a.mode === 'proxy')
    .reduce((n, a) => n + a.unit_ids.length, 0)
  const warnings =
    holder && settings
      ? proxyWarnings({
          settings,
          totalUnits,
          holderOwnUnits: own,
          holderProxyUnits: heldProxies,
          addUnits: owner.units.length,
        })
      : []
  return (
    <div className="flex flex-col gap-3 rounded-md bg-muted/50 p-3">
      <Field
        label={t`Represented by`}
        htmlFor={`proxy-${owner.personId}`}
        hint={
          settings
            ? t`Limit: ${proxyLimit(settings, totalUnits)} proxies per person (site settings).`
            : undefined
        }
      >
        <Select
          id={`proxy-${owner.personId}`}
          value={holder}
          onChange={(e) => setHolder(e.target.value)}
        >
          <option value="">{t`Choose a person`}</option>
          {persons
            .filter((p) => p.id !== owner.personId)
            .map((p) => (
              <option key={p.id} value={p.id}>
                {p.display_name}
              </option>
            ))}
        </Select>
      </Field>
      {settings ? (
        <ProxyWarnings warnings={warnings} limit={proxyLimit(settings, totalUnits)} />
      ) : null}
      <div>
        <Button size="lg" disabled={!holder || pending} onClick={() => onSubmit(holder)}>
          {warnings.length > 0 ? t`Record anyway` : t`Record proxy`}
        </Button>
      </div>
    </div>
  )
}
