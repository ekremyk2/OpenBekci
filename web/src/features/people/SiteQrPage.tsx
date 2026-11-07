import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { PrinterIcon, RefreshCwIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import { draftApi } from '@/api/draft'
import { useSession } from '@/app/session'
import { useIdempotencyKey, withIdempotencyKey } from '@/lib/idempotency'
import { ErrorState } from '@/components/data/ErrorState'
import { PageSkeleton } from '@/components/data/Skeletons'
import { PageHeader } from '@/components/layout/PageHeader'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { siteQueries } from '@/features/sites/queries'

/** QR code as inline SVG; the `qrcode` library loads only on this page. */
function QrCode({ value, label }: { value: string; label: string }) {
  const [svg, setSvg] = useState<string | null>(null)
  useEffect(() => {
    let live = true
    void import('qrcode').then(async (QR) => {
      const out = await QR.toString(value, { type: 'svg', margin: 1, errorCorrectionLevel: 'M' })
      if (live) setSvg(out)
    })
    return () => {
      live = false
    }
  }, [value])
  if (!svg)
    return (
      <div className="size-64 animate-pulse rounded bg-muted" role="status" aria-label={label} />
    )
  // The SVG comes from our own library call, but is rendered through an <img> data URL so no
  // markup is ever injected into the page (CSP and XSS policy).
  return (
    <img
      alt={label}
      className="size-64"
      src={`data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`}
    />
  )
}

export function SiteQrPage({ siteId }: { siteId: string }) {
  const { org } = useSession()
  const { t } = useLingui()
  const queryClient = useQueryClient()
  const idem = useIdempotencyKey()
  const orgId = org?.id ?? ''
  const siteQ = useQuery({ ...siteQueries.one(orgId, siteId), enabled: !!org })
  const codeQ = useQuery({
    queryKey: ['people.getJoinCode', orgId, siteId],
    enabled: !!org,
    queryFn: async () => {
      const { data } = await draftApi.GET('/v1/orgs/{org_id}/sites/{site_id}/join-code', {
        params: { path: { org_id: orgId, site_id: siteId } },
      })
      return data!
    },
  })
  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: ['people.getJoinCode', orgId, siteId] })
  const toggle = useMutation({
    mutationFn: (enabled: boolean) =>
      draftApi.PUT('/v1/orgs/{org_id}/sites/{site_id}/join-code', {
        params: { path: { org_id: orgId, site_id: siteId } },
        body: { enabled },
        ...withIdempotencyKey(crypto.randomUUID()),
      }),
    onSuccess: invalidate,
  })
  const rotate = useMutation({
    mutationFn: () =>
      draftApi.POST('/v1/orgs/{org_id}/sites/{site_id}/join-code:rotate', {
        params: { path: { org_id: orgId, site_id: siteId } },
        ...withIdempotencyKey(idem.key),
      }),
    onSuccess: async () => {
      idem.renew()
      await invalidate()
    },
  })

  const print = () => {
    document.body.setAttribute('data-print-poster', '')
    window.print()
    document.body.removeAttribute('data-print-poster')
  }

  if (codeQ.isLoading || siteQ.isLoading) return <PageSkeleton />
  if (codeQ.error) return <ErrorState error={codeQ.error} onRetry={() => void codeQ.refetch()} />
  const code = codeQ.data!
  const siteName = siteQ.data?.name ?? ''
  return (
    <div className="flex flex-col gap-6">
      <div data-no-print className="contents">
        <PageHeader
          title={<Trans>Site QR code</Trans>}
          crumbs={[
            { label: t`Sites`, to: '/orgs/$orgId/sites' },
            { label: siteName, to: '/orgs/$orgId/sites/$siteId', params: { siteId } },
            { label: t`QR code` },
          ]}
          description={
            <Trans>
              Print this poster for the notice board or share the link in the residents&apos; group.
              People who scan it sign up and ask for their unit; you approve each claim.
            </Trans>
          }
          actions={
            <>
              <Button variant="outline" onClick={() => rotate.mutate()} disabled={rotate.isPending}>
                <RefreshCwIcon aria-hidden="true" />
                <Trans>New code</Trans>
              </Button>
              <Button onClick={print} disabled={!code.enabled}>
                <PrinterIcon aria-hidden="true" />
                <Trans>Print poster</Trans>
              </Button>
            </>
          }
        />
        <label className="flex items-center gap-2 text-sm">
          <Checkbox
            checked={code.enabled}
            disabled={toggle.isPending}
            onChange={(e) => toggle.mutate(e.target.checked)}
          />
          <Trans>Residents can join with this code</Trans>
        </label>
        <p className="text-xs text-muted-foreground">
          <Trans>Creating a new code stops the old poster from working.</Trans>
        </p>
      </div>
      {code.enabled ? (
        <article
          aria-label={t`Poster`}
          className="mx-auto flex w-full max-w-xl flex-col items-center gap-4 rounded-lg border bg-white p-10 text-center text-black print:border-0"
        >
          <h2 className="text-3xl font-bold">{siteName}</h2>
          <p className="text-lg">
            <Trans>Scan to join your building&apos;s app</Trans>
          </p>
          <QrCode value={code.url} label={t`QR code to join ${siteName}`} />
          <p className="text-sm">
            <Trans>or go to</Trans>
          </p>
          <p className="break-all text-lg font-medium" dir="ltr">
            {code.url}
          </p>
          <p className="text-sm">
            <Trans>Code</Trans>:{' '}
            <span className="font-mono text-xl font-bold" dir="ltr">
              {code.code}
            </span>
          </p>
        </article>
      ) : (
        <p role="status" className="text-sm text-muted-foreground">
          <Trans>The code is switched off, so there is no poster to print.</Trans>
        </p>
      )}
    </div>
  )
}
