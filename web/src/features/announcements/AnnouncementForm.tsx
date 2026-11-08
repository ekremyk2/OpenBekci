import { lazy, Suspense, useMemo, useRef, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { useForm, useWatch, Controller } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Trans, useLingui } from '@lingui/react/macro'

import {
  draftApi,
  ifMatch,
  type Announcement,
  type AnnouncementAudience,
  type Channel,
} from '@/api/draft'
import { useSession } from '@/app/session'
import { FormError, FormField, SubmitButton, useApplyApiError } from '@/components/forms/FormField'
import { UploadField } from '@/components/files/UploadField'
import { useUploads } from '@/components/files/useUploads'
import { Button, buttonVariants } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Select } from '@/components/ui/select'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { useBlocks, useUnits } from '@/features/dashboard/reference'
import { useSite } from '@/features/dashboard/site'
import { usePayloadKeys, withIdempotencyKey } from '@/lib/idempotency'
import { must } from '@/lib/pagination'
import { isoToZonedLocal, zonedLocalToIso } from '@/lib/zoned'
import { toast } from '@/lib/toast'
import { DeliveryPreviewPanel, useDeliveryPreview } from './DeliveryPreview'

// The editor pulls in the Markdown renderer; readers of announcements never load it.
const MarkdownEditor = lazy(() => import('./MarkdownEditor'))

const CHANNELS: Channel[] = ['in_app', 'email', 'sms']

function AudiencePicker({
  orgId,
  siteId,
  audience,
  onChange,
}: {
  orgId: string
  siteId: string
  audience: AnnouncementAudience
  onChange: (a: AnnouncementAudience) => void
}) {
  const { t } = useLingui()
  const blocks = useBlocks(orgId, siteId, audience.kind === 'block')
  const units = useUnits(orgId, siteId, audience.kind === 'units')
  return (
    <div className="flex flex-col gap-2">
      <Select
        id="audience"
        aria-label={t`Audience`}
        value={audience.kind}
        onChange={(e) => onChange({ kind: e.target.value as AnnouncementAudience['kind'] })}
      >
        <option value="site">{t`Whole site`}</option>
        <option value="block">{t`One block`}</option>
        <option value="owners">{t`Owners only`}</option>
        <option value="tenants">{t`Tenants only`}</option>
        <option value="units">{t`Chosen units`}</option>
      </Select>
      {audience.kind === 'block' ? (
        <Select
          aria-label={t`Block`}
          value={audience.block_id ?? ''}
          onChange={(e) => onChange({ kind: 'block', block_id: e.target.value || undefined })}
        >
          <option value="">{t`Choose a block`}</option>
          {blocks.data?.map((b) => (
            <option key={b.id} value={b.id}>
              {b.name}
            </option>
          ))}
        </Select>
      ) : null}
      {audience.kind === 'units' ? (
        <fieldset className="flex max-h-48 flex-col gap-1 overflow-y-auto rounded-md border p-2">
          <legend className="px-1 text-sm">
            <Trans>Units</Trans>
          </legend>
          {units.data?.map((u) => {
            const checked = audience.unit_ids?.includes(u.id) ?? false
            return (
              <Label key={u.id} className="gap-2 font-normal">
                <Checkbox
                  checked={checked}
                  onChange={(e) => {
                    const set = new Set(audience.unit_ids ?? [])
                    if (e.target.checked) set.add(u.id)
                    else set.delete(u.id)
                    onChange({ kind: 'units', unit_ids: [...set] })
                  }}
                />
                {u.door_number}
              </Label>
            )
          })}
        </fieldset>
      ) : null}
    </div>
  )
}

export function AnnouncementForm({ existing }: { existing?: Announcement }) {
  const { t } = useLingui()
  const { org } = useSession()
  const { site } = useSite()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const orgId = org?.id ?? ''
  const tz = site?.timezone ?? 'UTC'
  const applyError = useApplyApiError()
  // Keys follow their payload: a retry of the same request replays its key, an edited payload gets
  // a new one (one key with two bodies is 422 idempotency_mismatch).
  const keyFor = usePayloadKeys()
  // What the server holds for this form once a save went through, and the payload it was saved with.
  const saved = useRef<{ announcement: Announcement; fingerprint: string } | null>(null)
  const [intent, setIntent] = useState<'draft' | 'publish'>('draft')

  const schema = useMemo(
    () =>
      z
        .object({
          title: z
            .string()
            .trim()
            .min(1, t`Enter a title`)
            .max(160, t`Keep the title short`),
          body_md: z
            .string()
            .trim()
            .min(1, t`Write the announcement`),
          audience: z.object({
            kind: z.enum(['site', 'block', 'owners', 'tenants', 'units']),
            block_id: z.string().optional(),
            unit_ids: z.array(z.string()).optional(),
          }),
          channels: z.array(z.enum(['in_app', 'email', 'sms', 'push'])).min(1, t`Choose a channel`),
          pinned: z.boolean(),
          publish_at: z.string(),
          expires_at: z.string(),
        })
        .superRefine((v, ctx) => {
          if (v.audience.kind === 'block' && !v.audience.block_id) {
            ctx.addIssue({ code: 'custom', path: ['audience'], message: t`Choose a block` })
          }
          if (v.audience.kind === 'units' && !(v.audience.unit_ids?.length ?? 0)) {
            ctx.addIssue({
              code: 'custom',
              path: ['audience'],
              message: t`Choose at least one unit`,
            })
          }
          if (v.publish_at && v.expires_at && v.expires_at <= v.publish_at) {
            ctx.addIssue({
              code: 'custom',
              path: ['expires_at'],
              message: t`The expiry must be after the publish time`,
            })
          }
        }),
    [t],
  )
  type Values = z.infer<typeof schema>
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: {
      title: existing?.title ?? '',
      body_md: existing?.body_md ?? '',
      audience: (existing?.audience as Values['audience']) ?? { kind: 'site' },
      channels: (existing?.channels as Values['channels']) ?? ['in_app'],
      pinned: existing?.pinned ?? false,
      publish_at: existing?.publish_at ? isoToZonedLocal(existing.publish_at, tz) : '',
      expires_at: existing?.expires_at ? isoToZonedLocal(existing.expires_at, tz) : '',
    },
  })
  const uploads = useUploads({
    orgId,
    siteId: site?.id,
    scope: 'announcement',
    profile: 'document',
    maxFiles: 10,
  })

  const audience = useWatch({ control: form.control, name: 'audience' })
  const channels = useWatch({ control: form.control, name: 'channels' })
  const preview = useDeliveryPreview({
    orgId,
    siteId: site?.id,
    audience: audience as AnnouncementAudience,
    channels: channels as Channel[],
  })
  const publishAt = useWatch({ control: form.control, name: 'publish_at' })
  const scheduled = !!publishAt && zonedLocalToIso(publishAt, tz) > new Date().toISOString()

  const save = useMutation({
    mutationFn: async (v: Values): Promise<Announcement> => {
      const fileIds = [...(existing?.file_ids ?? []), ...uploads.files.map((f) => f.id)]
      const payload = {
        title: v.title.trim(),
        body_md: v.body_md,
        audience: v.audience as AnnouncementAudience,
        channels: v.channels,
        pinned: v.pinned,
        publish_at: v.publish_at ? zonedLocalToIso(v.publish_at, tz) : null,
        expires_at: v.expires_at ? zonedLocalToIso(v.expires_at, tz) : null,
        file_ids: fileIds,
      }
      const fingerprint = JSON.stringify(payload)

      // Step 1, save. After a save has gone through, a retry never creates again: it skips the
      // save when nothing changed and PATCHes the saved record when the person edited meanwhile.
      const held = saved.current?.announcement ?? existing
      let record: Announcement
      if (saved.current && saved.current.fingerprint === fingerprint) {
        record = saved.current.announcement
      } else if (held) {
        record = must(
          await draftApi.PATCH('/v1/orgs/{org_id}/announcements/{announcement_id}', {
            params: {
              path: { org_id: orgId, announcement_id: held.id },
              header: ifMatch(held.version),
            },
            body: payload,
          }),
        )
        saved.current = { announcement: record, fingerprint }
      } else {
        const body = { site_id: site!.id, ...payload }
        record = must(
          await draftApi.POST('/v1/orgs/{org_id}/announcements', {
            params: { path: { org_id: orgId } },
            body,
            ...withIdempotencyKey(keyFor('create', body)),
          }),
        )
        saved.current = { announcement: record, fingerprint }
      }

      // Step 2, publish. Its own key, tied to the record and the channels being sent.
      if (intent === 'publish' && !scheduled && record.status !== 'published') {
        const publishBody = { channels: v.channels }
        record = must(
          await draftApi.POST('/v1/orgs/{org_id}/announcements/{announcement_id}:publish', {
            params: {
              path: { org_id: orgId, announcement_id: record.id },
              header: {
                'Idempotency-Key': keyFor('publish', { id: record.id, ...publishBody }),
              },
            },
            body: publishBody,
          }),
        )
        saved.current = { announcement: record, fingerprint }
      }
      return record
    },
    onSuccess: async (result) => {
      await qc.invalidateQueries({ queryKey: ['operations.listAnnouncements'] })
      await qc.invalidateQueries({ queryKey: ['operations.getAnnouncement'] })
      toast.info(
        result.status === 'published'
          ? t`Announcement published.`
          : result.status === 'scheduled'
            ? t`Announcement scheduled.`
            : t`Draft saved.`,
      )
      await navigate({
        to: '/orgs/$orgId/announcements/$announcementId',
        params: { orgId, announcementId: result.id },
      })
    },
    onError: (e) => applyError(form, e),
  })

  const errors = form.formState.errors
  const locked = existing && (existing.status === 'published' || existing.status === 'archived')
  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-4">
      <h1 className="text-2xl font-semibold">
        {existing ? <Trans>Edit announcement</Trans> : <Trans>New announcement</Trans>}
      </h1>
      <form
        className="flex flex-col gap-4"
        noValidate
        onSubmit={(e) => void form.handleSubmit((v) => save.mutate(v))(e)}
      >
        <FormField id="title" label={<Trans>Title</Trans>} error={errors.title?.message}>
          <Input id="title" aria-invalid={!!errors.title} {...form.register('title')} />
        </FormField>

        <FormField
          id="body"
          label={<Trans>Message (Markdown)</Trans>}
          error={errors.body_md?.message}
        >
          <Controller
            control={form.control}
            name="body_md"
            render={({ field }) => (
              <Suspense fallback={<Skeleton className="h-60 w-full" />}>
                <MarkdownEditor
                  id="body"
                  value={field.value}
                  onChange={field.onChange}
                  invalid={!!errors.body_md}
                />
              </Suspense>
            )}
          />
        </FormField>

        <FormField
          id="audience"
          label={<Trans>Audience</Trans>}
          error={(errors.audience as { message?: string } | undefined)?.message}
        >
          <Controller
            control={form.control}
            name="audience"
            render={({ field }) => (
              <AudiencePicker
                orgId={orgId}
                siteId={site?.id ?? ''}
                audience={field.value as AnnouncementAudience}
                onChange={field.onChange}
              />
            )}
          />
        </FormField>

        <fieldset className="flex flex-col gap-2">
          <legend className="text-sm font-medium">
            <Trans>Channels</Trans>
          </legend>
          <div className="flex flex-wrap gap-4">
            {CHANNELS.map((c) => (
              <Label key={c} className="gap-2 font-normal">
                <Checkbox
                  checked={channels.includes(c)}
                  onChange={(e) =>
                    form.setValue(
                      'channels',
                      e.target.checked ? [...channels, c] : channels.filter((x) => x !== c),
                      { shouldValidate: true },
                    )
                  }
                />
                {c === 'in_app' ? t`In-app` : c === 'email' ? t`Email` : t`SMS`}
              </Label>
            ))}
          </div>
          {errors.channels?.message ? (
            <p role="alert" className="text-sm text-destructive">
              {errors.channels.message}
            </p>
          ) : null}
          <DeliveryPreviewPanel preview={preview} channels={channels as Channel[]} />
        </fieldset>

        <div className="grid gap-4 sm:grid-cols-2">
          <FormField
            id="publish_at"
            label={<Trans>Publish at (optional)</Trans>}
            hint={<Trans>Times are in the site's timezone ({tz}). Empty publishes now.</Trans>}
          >
            <Input id="publish_at" type="datetime-local" {...form.register('publish_at')} />
          </FormField>
          <FormField
            id="expires_at"
            label={<Trans>Expires at (optional)</Trans>}
            error={errors.expires_at?.message}
          >
            <Input id="expires_at" type="datetime-local" {...form.register('expires_at')} />
          </FormField>
        </div>

        <Label className="gap-2">
          <Checkbox {...form.register('pinned')} />
          <Trans>Pin to the top</Trans>
        </Label>

        <UploadField
          controller={uploads}
          orgId={orgId}
          label={<Trans>Attach files</Trans>}
          hint={<Trans>PDF, images and office documents.</Trans>}
        />

        <FormError message={errors.root?.message} />
        <div className="flex flex-wrap gap-2">
          {!locked ? (
            <>
              <SubmitButton
                busy={save.isPending && intent === 'publish'}
                // O5: no publish (or schedule) without the counts for what is chosen now
                disabled={uploads.busy || !site || !preview.current}
                onClick={() => setIntent('publish')}
              >
                {scheduled ? <Trans>Schedule</Trans> : <Trans>Publish</Trans>}
              </SubmitButton>
              <Button
                type="submit"
                variant="outline"
                disabled={uploads.busy || save.isPending || !site}
                onClick={() => setIntent('draft')}
              >
                <Trans>Save draft</Trans>
              </Button>
            </>
          ) : (
            <SubmitButton busy={save.isPending} onClick={() => setIntent('draft')}>
              <Trans>Save changes</Trans>
            </SubmitButton>
          )}
          <Link
            to="/orgs/$orgId/announcements"
            params={{ orgId }}
            className={buttonVariants({ variant: 'ghost' })}
          >
            <Trans>Cancel</Trans>
          </Link>
        </div>
      </form>
    </div>
  )
}
