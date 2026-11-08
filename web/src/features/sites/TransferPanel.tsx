import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Trans, useLingui } from '@lingui/react/macro'
import { draftApi, multipart, type Schema } from '@/api/draft'
import { runWithReauth } from '@/app/reauth'
import { useIdempotencyKey, withIdempotencyKey } from '@/lib/idempotency'
import { SiteDate } from '@/components/data/SiteDate'
import { Field, FormError, SubmitButton, useApplyFormError } from '@/components/forms/FormBits'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
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
import { JobProgress } from '@/features/import/useJob'
import { siteQueries } from './queries'

/** Site export, import into this organization and offboarding (D2); all re-authentication protected. */
export function TransferPanel({
  orgId,
  site,
  canExport,
  canOffboard,
}: {
  orgId: string
  site: Schema<'Site'>
  canExport: boolean
  canOffboard: boolean
}) {
  const { t } = useLingui()
  const queryClient = useQueryClient()
  const idem = useIdempotencyKey()
  const [jobId, setJobId] = useState<string | null>(null)
  const [docs, setDocs] = useState(true)
  const [money, setMoney] = useState(true)
  const [offboardOpen, setOffboardOpen] = useState(false)
  const transfers = useQuery(siteQueries.transfers(orgId))

  const exportSite = useMutation({
    mutationFn: () =>
      runWithReauth(() =>
        draftApi.POST('/v1/orgs/{org_id}/sites/{site_id}:export', {
          params: { path: { org_id: orgId, site_id: site.id } },
          body: { include_documents: docs, include_money_history: money },
          ...withIdempotencyKey(idem.key),
        }),
      ),
    onSuccess: async ({ data }) => {
      idem.renew()
      setJobId(data?.job_id ?? null)
      await queryClient.invalidateQueries({ queryKey: ['tenancy.listSiteTransfers', orgId] })
    },
  })
  const importSite = useMutation({
    mutationFn: (archive: File) =>
      runWithReauth(() =>
        draftApi.POST('/v1/orgs/{org_id}/sites:import', {
          params: { path: { org_id: orgId } },
          body: {} as never,
          ...multipart({ archive }),
          ...withIdempotencyKey(idem.key),
        }),
      ),
    onSuccess: ({ data }) => {
      idem.renew()
      setJobId(data?.job_id ?? null)
    },
  })

  return (
    <div className="grid max-w-2xl gap-6">
      {canExport ? (
        <section className="grid gap-3" aria-labelledby="exp-h">
          <h2 id="exp-h" className="text-base font-semibold">
            <Trans>Export this site</Trans>
          </h2>
          <p className="text-sm text-muted-foreground">
            <Trans>
              One archive with people, occupancies, money history, documents and decisions. You will
              be asked to confirm it is you.
            </Trans>
          </p>
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={docs} onChange={(e) => setDocs(e.target.checked)} />
            <Trans>Include documents</Trans>
          </label>
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={money} onChange={(e) => setMoney(e.target.checked)} />
            <Trans>Include money history</Trans>
          </label>
          <div className="flex flex-wrap items-center gap-2">
            <Button onClick={() => exportSite.mutate()} disabled={exportSite.isPending}>
              <Trans>Export site</Trans>
            </Button>
            <label className="text-sm">
              <span className="sr-only">
                <Trans>Archive file to import</Trans>
              </span>
              <input
                type="file"
                accept=".zip,.obsite,application/zip"
                aria-label={t`Archive file to import`}
                className="text-sm"
                onChange={(e) => {
                  const f = e.target.files?.[0]
                  if (f) importSite.mutate(f)
                }}
              />
            </label>
          </div>
          {exportSite.error || importSite.error ? (
            <FormError message={t`The transfer could not be started. Please try again.`} />
          ) : null}
          {jobId ? <JobProgress orgId={orgId} jobId={jobId} /> : null}
        </section>
      ) : null}

      {transfers.data && transfers.data.items.length > 0 ? (
        <section className="grid gap-2" aria-labelledby="tr-h">
          <h2 id="tr-h" className="text-base font-semibold">
            <Trans>Recent transfers</Trans>
          </h2>
          <ul className="divide-y rounded-md border text-sm">
            {transfers.data.items.map((tr) => (
              <li key={tr.id} className="flex items-center justify-between gap-2 p-3">
                <span>{tr.direction}</span>
                <SiteDate value={tr.at} timeZone={site.timezone} kind="datetime" />
                <Badge variant={tr.status === 'failed' ? 'destructive' : 'secondary'}>
                  {tr.status}
                </Badge>
              </li>
            ))}
          </ul>
        </section>
      ) : null}

      {canOffboard ? (
        <section
          className="grid gap-3 rounded-md border border-destructive/40 p-4"
          aria-labelledby="off-h"
        >
          <h2 id="off-h" className="text-base font-semibold text-destructive">
            <Trans>Offboard this site</Trans>
          </h2>
          <p className="text-sm text-muted-foreground">
            <Trans>
              Hands the site over and removes this organization&apos;s access to it. Export it
              first.
            </Trans>
          </p>
          <div>
            <Button
              variant="destructive"
              disabled={site.status !== 'active'}
              onClick={() => setOffboardOpen(true)}
            >
              <Trans>Offboard site…</Trans>
            </Button>
          </div>
          <OffboardDialog
            orgId={orgId}
            site={site}
            open={offboardOpen}
            onOpenChange={setOffboardOpen}
            onStarted={(id) => setJobId(id)}
          />
        </section>
      ) : null}
    </div>
  )
}

function OffboardDialog({
  orgId,
  site,
  open,
  onOpenChange,
  onStarted,
}: {
  orgId: string
  site: Schema<'Site'>
  open: boolean
  onOpenChange: (open: boolean) => void
  onStarted: (jobId: string | null) => void
}) {
  const { t } = useLingui()
  const queryClient = useQueryClient()
  const idem = useIdempotencyKey()
  const applyError = useApplyFormError()
  const schema = useMemo(
    () =>
      z.object({
        confirm_site_name: z
          .string()
          .refine((v) => v === site.name, t`Type the site name exactly to confirm`),
        reason: z.string().trim(),
      }),
    [t, site.name],
  )
  const form = useForm({
    resolver: zodResolver(schema),
    defaultValues: { confirm_site_name: '', reason: '' },
  })
  const offboard = useMutation({
    mutationFn: (v: z.infer<typeof schema>) =>
      runWithReauth(() =>
        draftApi.POST('/v1/orgs/{org_id}/sites/{site_id}:offboard', {
          params: { path: { org_id: orgId, site_id: site.id } },
          body: { confirm_site_name: v.confirm_site_name, reason: v.reason || undefined },
          ...withIdempotencyKey(idem.key),
        }),
      ),
    onSuccess: async ({ data }) => {
      onStarted(data?.job_id ?? null)
      onOpenChange(false)
      await queryClient.invalidateQueries({ queryKey: ['tenancy.getSite', orgId] })
    },
    onError: (e) => applyError(form, e),
  })
  const { errors } = form.formState
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <form
          noValidate
          className="grid gap-4"
          onSubmit={form.handleSubmit((v) => offboard.mutateAsync(v).catch(() => undefined))}
        >
          <DialogHeader>
            <DialogTitle>
              <Trans>Offboard {site.name}?</Trans>
            </DialogTitle>
            <DialogDescription>
              <Trans>This cannot be undone from here. Type the site name to continue.</Trans>
            </DialogDescription>
          </DialogHeader>
          <Field
            id="off-name"
            label={<Trans>Site name</Trans>}
            error={errors.confirm_site_name?.message}
          >
            <Input
              id="off-name"
              autoComplete="off"
              aria-invalid={!!errors.confirm_site_name}
              {...form.register('confirm_site_name')}
            />
          </Field>
          <Field id="off-reason" label={<Trans>Reason (optional)</Trans>}>
            <Input id="off-reason" {...form.register('reason')} />
          </Field>
          <FormError message={errors.root?.message} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              <Trans>Cancel</Trans>
            </Button>
            <SubmitButton busy={offboard.isPending} variant="destructive">
              <Trans>Offboard site</Trans>
            </SubmitButton>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
