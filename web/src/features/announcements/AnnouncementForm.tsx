import { lazy, Suspense, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { useForm, useWatch, Controller } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Trans, useLingui } from '@lingui/react/macro'
import { LoaderCircleIcon } from 'lucide-react'
import {
  apiDraft,
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
import { Checkbox, Select } from '@/components/ui/select'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { useBlocks, useUnits } from '@/features/dashboard/reference'
import { useSite } from '@/features/dashboard/site'
import { useIdempotencyKey, withIdempotencyKey } from '@/lib/idempotency'
import { must } from '@/lib/pagination'
import { isoToZonedLocal, zonedLocalToIso } from '@/lib/zoned'
import { toast } from '@/lib/toast'

// The editor pulls in the Markdown renderer; readers of announcements never load it.
const MarkdownEditor = lazy(() => import('./MarkdownEditor'))

const CHANNELS: Channel[] = ['in_app', 'email', 'sms']

/** Recipient and SMS counts for the audience and channels chosen so far (O5), fetched before publish. */
export function ChannelPreview({
  orgId,
  siteId,
  audience,
  channels,
}: {
  orgId: string
  siteId: string
  audience: AnnouncementAudience
  channels: Channel[]
}) {
  const { t } = useLingui()
  const idem = useIdempotencyKey()
  const ready = audience.kind !== 'units' || (audience.unit_ids?.length ?? 0) > 0
  const preview = useQuery({
    queryKey: ['operations.previewAnnouncement', orgId, siteId, audience, [...channels].sort()],
    enabled: ready && channels.length > 0,
    staleTime: 0,
    queryFn: async () =>
      must(
        await apiDraft.POST('/v1/orgs/{org_id}/announcements:preview', {
          params: { path: { org_id: orgId } },
          body: { site_id: siteId, audience, channels },
          ...withIdempotencyKey(idem.key),
        }),
      ),
  })
  return (
    <div
      role="status"
      aria-live="polite"
      aria-label={t`Delivery preview`}
      className="rounded-md border bg-muted/40 p-3 text-sm"
    >
      {!ready || channels.length === 0 ? (
        <p className="text-muted-foreground">
          <Trans>Choose an audience and at least one channel to see who will be reached.</Trans>
        </p>
      ) : preview.isPending ? (
        <p className="flex items-center gap-2 text-muted-foreground">
          <LoaderCircleIcon className="size-4 animate-spin" aria-hidden="true" />
          <Trans>Counting recipients</Trans>
        </p>
      ) : preview.isError ? (
        <p className="text-destructive">
          <Trans>Could not count the recipients.</Trans>
        </p>
      ) : (
        <ul className="flex flex-col gap-1">
          <li>
            <Trans>Recipients: {preview.data.recipient_count}</Trans>
          </li>
          {channels.includes('in_app') ? (
            <li>
              <Trans>In-app notifications: {preview.data.in_app_count}</Trans>
            </li>
          ) : null}
          {channels.includes('email') ? (
            <li>
              <Trans>Emails: {preview.data.email_count}</Trans>
            </li>
          ) : null}
          {channels.includes('sms') ? (
            <li className="font-medium">
              <Trans>SMS messages: {preview.data.sms_count} (billed to the site)</Trans>
            </li>
          ) : null}
        </ul>
      )}
    </div>
  )
}

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
  const createKey = useIdempotencyKey()
  const publishKey = useIdempotencyKey()
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
      let saved: Announcement
      if (existing) {
        saved = must(
          await apiDraft.PATCH('/v1/orgs/{org_id}/announcements/{announcement_id}', {
            params: {
              path: { org_id: orgId, announcement_id: existing.id },
              header: ifMatch(existing.version),
            },
            body: payload,
          }),
        )
      } else {
        saved = must(
          await apiDraft.POST('/v1/orgs/{org_id}/announcements', {
            params: { path: { org_id: orgId } },
            body: { site_id: site!.id, ...payload },
            ...withIdempotencyKey(createKey.key),
          }),
        )
      }
      if (intent === 'publish' && !scheduled) {
        saved = must(
          await apiDraft.POST('/v1/orgs/{org_id}/announcements/{announcement_id}:publish', {
            params: { path: { org_id: orgId, announcement_id: saved.id } },
            body: { channels: v.channels },
            ...withIdempotencyKey(publishKey.key),
          }),
        )
      }
      return saved
    },
    onSuccess: async (saved) => {
      await qc.invalidateQueries({ queryKey: ['operations.listAnnouncements'] })
      await qc.invalidateQueries({ queryKey: ['operations.getAnnouncement'] })
      toast.info(
        saved.status === 'published'
          ? t`Announcement published.`
          : saved.status === 'scheduled'
            ? t`Announcement scheduled.`
            : t`Draft saved.`,
      )
      await navigate({
        to: '/orgs/$orgId/announcements/$announcementId',
        params: { orgId, announcementId: saved.id },
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
          {site ? (
            <ChannelPreview
              orgId={orgId}
              siteId={site.id}
              audience={audience as AnnouncementAudience}
              channels={channels as Channel[]}
            />
          ) : null}
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
                disabled={uploads.busy || !site}
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
