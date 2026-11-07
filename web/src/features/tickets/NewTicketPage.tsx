import { useMemo } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { useForm, useWatch } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Trans, useLingui } from '@lingui/react/macro'
import { apiDraft } from '@/api/draft'
import { useSession } from '@/app/session'
import { UploadField } from '@/components/files/UploadField'
import { useUploads } from '@/components/files/useUploads'
import { FormError, FormField, SubmitButton, useApplyApiError } from '@/components/forms/FormField'
import { buttonVariants } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { useUnits } from '@/features/dashboard/reference'
import { SitePicker, useSite } from '@/features/dashboard/site'
import { useIdempotencyKey, withIdempotencyKey } from '@/lib/idempotency'
import { must } from '@/lib/pagination'
import { useCategories } from './queries'

export function NewTicketPage() {
  const { t } = useLingui()
  const { org, me, can } = useSession()
  const { site } = useSite()
  const navigate = useNavigate()
  const orgId = org?.id ?? ''
  const canManage = can('tickets.manage')
  const applyError = useApplyApiError()
  const idem = useIdempotencyKey()

  const categories = useCategories(orgId, site?.id)
  const allUnits = useUnits(orgId, site?.id, canManage)
  const persons = useQuery({
    queryKey: ['people.listPersons', orgId, site?.id],
    enabled: canManage && !!site,
    queryFn: async () =>
      must(
        await apiDraft.GET('/v1/orgs/{org_id}/persons', {
          params: { path: { org_id: orgId }, query: { site_id: site!.id, limit: 200 } },
        }),
      ).items,
  })
  const myUnits = (me?.occupancies ?? []).filter(
    (o) => o.organization_id === orgId && o.site_id === site?.id,
  )

  const schema = useMemo(
    () =>
      z.object({
        category_id: z.string().min(1, t`Choose a category`),
        title: z
          .string()
          .trim()
          .min(3, t`Enter a short title`)
          .max(140, t`Keep the title short`),
        description: z
          .string()
          .trim()
          .min(1, t`Describe the problem`),
        unit_id: z.string().optional(),
        visibility: z.enum(['', 'private', 'common']),
        reporter_person_id: z.string().optional(),
      }),
    [t],
  )
  type Values = z.infer<typeof schema>
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: {
      category_id: '',
      title: '',
      description: '',
      unit_id: '',
      visibility: '',
      reporter_person_id: '',
    },
  })
  const uploads = useUploads({
    orgId,
    siteId: site?.id,
    scope: 'ticket',
    profile: 'photo',
    maxFiles: 10,
    downscale: true,
  })

  const categoryId = useWatch({ control: form.control, name: 'category_id' })
  const category = categories.data?.find((c) => c.id === categoryId)
  const defaultVisibility = category?.default_visibility

  const create = useMutation({
    mutationFn: async (v: Values) =>
      must(
        await apiDraft.POST('/v1/orgs/{org_id}/tickets', {
          params: { path: { org_id: orgId } },
          body: {
            site_id: site!.id,
            category_id: v.category_id,
            title: v.title.trim(),
            description: v.description.trim(),
            unit_id: v.unit_id || null,
            visibility: v.visibility || undefined,
            reporter_person_id: v.reporter_person_id || undefined,
            attachment_file_ids: uploads.files.map((f) => f.id),
          },
          ...withIdempotencyKey(idem.key),
        }),
      ),
    onSuccess: (ticket) =>
      navigate({ to: '/orgs/$orgId/tickets/$ticketId', params: { orgId, ticketId: ticket.id } }),
    onError: (e) => applyError(form, e),
  })

  const errors = form.formState.errors
  return (
    <div className="mx-auto flex max-w-2xl flex-col gap-4">
      <div className="flex items-center gap-2">
        <h1 className="me-auto text-2xl font-semibold">
          <Trans>New ticket</Trans>
        </h1>
        <SitePicker />
      </div>
      <form
        className="flex flex-col gap-4"
        noValidate
        onSubmit={(e) => void form.handleSubmit((v) => create.mutate(v))(e)}
      >
        {canManage ? (
          <FormField
            id="reporter"
            label={<Trans>Opened on behalf of</Trans>}
            hint={<Trans>Leave empty to open it as yourself.</Trans>}
          >
            <Select id="reporter" {...form.register('reporter_person_id')}>
              <option value="">{t`Myself`}</option>
              {persons.data?.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.display_name}
                </option>
              ))}
            </Select>
          </FormField>
        ) : null}

        <FormField
          id="category"
          label={<Trans>Category</Trans>}
          error={errors.category_id?.message}
        >
          <Select
            id="category"
            aria-invalid={!!errors.category_id}
            {...form.register('category_id')}
          >
            <option value="">{t`Choose a category`}</option>
            {categories.data?.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </Select>
        </FormField>

        <FormField id="title" label={<Trans>Title</Trans>} error={errors.title?.message}>
          <Input id="title" aria-invalid={!!errors.title} {...form.register('title')} />
        </FormField>

        <FormField
          id="description"
          label={<Trans>Description</Trans>}
          error={errors.description?.message}
        >
          <Textarea
            id="description"
            rows={5}
            aria-invalid={!!errors.description}
            {...form.register('description')}
          />
        </FormField>

        <FormField
          id="unit"
          label={<Trans>Unit (optional)</Trans>}
          hint={<Trans>Leave empty for problems in shared areas.</Trans>}
        >
          <Select id="unit" {...form.register('unit_id')}>
            <option value="">{t`No unit`}</option>
            {canManage
              ? allUnits.data?.map((u) => (
                  <option key={u.id} value={u.id}>
                    {u.door_number}
                  </option>
                ))
              : myUnits.map((o) => (
                  <option key={o.unit_id} value={o.unit_id}>
                    {o.unit_label}
                  </option>
                ))}
          </Select>
        </FormField>

        {canManage ? (
          <FormField id="visibility" label={<Trans>Visibility</Trans>}>
            <Select id="visibility" {...form.register('visibility')}>
              <option value="">
                {defaultVisibility === 'common'
                  ? t`Category default (common)`
                  : defaultVisibility === 'private'
                    ? t`Category default (private)`
                    : t`Category default`}
              </option>
              <option value="private">{t`Private: that unit and management`}</option>
              <option value="common">{t`Common: the whole site`}</option>
            </Select>
          </FormField>
        ) : defaultVisibility ? (
          <p className="text-sm text-muted-foreground">
            {defaultVisibility === 'common' ? (
              <Trans>This will be a common ticket, visible to the whole site.</Trans>
            ) : (
              <Trans>This ticket is private: only your unit and management see it.</Trans>
            )}
          </p>
        ) : null}

        <UploadField
          controller={uploads}
          orgId={orgId}
          label={<Trans>Add photos</Trans>}
          hint={
            <Trans>Up to 10 photos. Large photos are reduced and location data is removed.</Trans>
          }
        />

        <FormError message={errors.root?.message} />
        <div className="flex gap-2">
          <SubmitButton busy={create.isPending} disabled={uploads.busy || !site}>
            <Trans>Open ticket</Trans>
          </SubmitButton>
          <Link
            to="/orgs/$orgId/tickets"
            params={{ orgId }}
            className={buttonVariants({ variant: 'outline' })}
          >
            <Trans>Cancel</Trans>
          </Link>
        </div>
      </form>
    </div>
  )
}
