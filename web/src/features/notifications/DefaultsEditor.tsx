import { useState } from 'react'
import { AccessDenied } from '@/components/data/AccessDenied'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Trans, useLingui } from '@lingui/react/macro'
import { draftApi, ifMatch, type Channel } from '@/api/draft'
import { useSession } from '@/app/session'
import { ErrorState } from '@/components/data/ErrorState'
import { TableSkeleton } from '@/components/data/Skeletons'
import { FormError } from '@/components/forms/FormField'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { SitePicker, useSite } from '@/features/dashboard/site'
import { useApplyErrorMessage } from '@/features/tickets/errors'
import { must } from '@/lib/pagination'
import { toast } from '@/lib/toast'
import { notificationTypeLabel, orderedTypes } from './types'

const CHANNELS: Channel[] = ['in_app', 'email', 'sms']

/** Site defaults for automatic messages (O5): which channels each kind of message uses. */
export function DefaultsEditor() {
  const { t, i18n } = useLingui()
  const { org, can } = useSession()
  const { site } = useSite()
  const qc = useQueryClient()
  const describe = useApplyErrorMessage()
  const orgId = org?.id ?? ''
  const siteId = site?.id
  const allowed = can('notifications.manage')
  // edits made on top of the server values; empty after a save
  const [rows, setRows] = useState<Record<string, Channel[]>>({})
  const [error, setError] = useState<string>()

  const query = useQuery({
    queryKey: ['operations.getNotificationDefaults', orgId, siteId],
    enabled: allowed && !!siteId,
    queryFn: async () =>
      must(
        await draftApi.GET('/v1/orgs/{org_id}/notification-defaults', {
          params: { path: { org_id: orgId }, query: { site_id: siteId! } },
        }),
      ),
  })
  const usage = useQuery({
    queryKey: ['operations.getSmsUsage', orgId, siteId],
    enabled: allowed && !!siteId,
    queryFn: async () =>
      must(
        await draftApi.GET('/v1/orgs/{org_id}/sms-usage', {
          params: { path: { org_id: orgId }, query: { site_id: siteId! } },
        }),
      ),
  })
  const save = useMutation({
    mutationFn: async () =>
      must(
        await draftApi.PUT('/v1/orgs/{org_id}/notification-defaults', {
          params: {
            path: { org_id: orgId },
            query: { site_id: siteId! },
            header: ifMatch(query.data!.version),
          },
          body: {
            defaults: (query.data?.defaults ?? [])
              .filter((d) => !d.locked)
              .map((d) => ({ type: d.type, channels: rows[d.type] ?? d.channels })),
          },
        }),
      ),
    onSuccess: async () => {
      setError(undefined)
      setRows({})
      await qc.invalidateQueries({ queryKey: ['operations.getNotificationDefaults'] })
      toast.info(t`Defaults saved.`)
    },
    onError: (e) => setError(describe(e)),
  })

  if (!allowed) {
    return <AccessDenied heading="h2" />
  }
  if (query.isPending) return <TableSkeleton rows={6} columns={4} />
  if (query.isError) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />

  const defaults = query.data.defaults
  const dirty = defaults.some(
    (d) =>
      JSON.stringify([...(rows[d.type] ?? d.channels)].sort()) !==
      JSON.stringify([...d.channels].sort()),
  )
  const channelLabel = (c: Channel) =>
    c === 'in_app' ? t`In-app` : c === 'email' ? t`Email` : t`SMS`

  return (
    <div className="flex max-w-3xl flex-col gap-4">
      <div className="flex items-center gap-2">
        <h2 className="me-auto text-lg font-semibold">
          <Trans>Automatic messages</Trans>
        </h2>
        <SitePicker />
      </div>
      <p className="text-sm text-muted-foreground">
        <Trans>
          Messages nobody sends by hand use these channels. Sign-in codes and invitations always go
          by SMS or email. People can still mute non-essential types themselves.
        </Trans>
      </p>
      {usage.data ? (
        <p role="status" className="rounded-md border bg-muted/40 p-3 text-sm">
          <Trans>
            SMS this month: {usage.data.sent} sent, {usage.data.delivered} delivered,{' '}
            {usage.data.failed} failed
            {usage.data.monthly_limit ? ` (limit ${usage.data.monthly_limit})` : ''}
          </Trans>
        </p>
      ) : null}
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b text-start">
            <th scope="col" className="py-2 text-start font-medium">
              <Trans>Message</Trans>
            </th>
            {CHANNELS.map((c) => (
              <th key={c} scope="col" className="py-2 text-center font-medium">
                {channelLabel(c)}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {orderedTypes(defaults.map((d) => d.type))
            .filter((type) => defaults.some((d) => d.type === type))
            .map((type) => {
              const d = defaults.find((x) => x.type === type)!
              const current = rows[type] ?? (d.channels as Channel[])
              return (
                <tr key={type} className="border-b">
                  <th scope="row" className="py-2 text-start font-normal">
                    {notificationTypeLabel(type, i18n)}
                    {d.locked ? (
                      <span className="ms-2 text-xs text-muted-foreground">
                        <Trans>Fixed</Trans>
                      </span>
                    ) : null}
                  </th>
                  {CHANNELS.map((c) => (
                    <td key={c} className="py-2 text-center">
                      <Checkbox
                        aria-label={`${notificationTypeLabel(type, i18n)}: ${channelLabel(c)}`}
                        checked={current.includes(c)}
                        disabled={d.locked}
                        onChange={(e) =>
                          setRows((r) => ({
                            ...r,
                            [type]: e.target.checked
                              ? [...current, c]
                              : current.filter((x) => x !== c),
                          }))
                        }
                      />
                    </td>
                  ))}
                </tr>
              )
            })}
        </tbody>
      </table>
      <FormError message={error} />
      <Button className="w-fit" disabled={!dirty || save.isPending} onClick={() => save.mutate()}>
        <Trans>Save defaults</Trans>
      </Button>
    </div>
  )
}
