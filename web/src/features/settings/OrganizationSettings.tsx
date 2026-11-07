import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Trans, useLingui } from '@lingui/react/macro'
import { LockIcon } from 'lucide-react'
import { apiDraft, ifMatch } from '@/api/draft'
import { useSession } from '@/app/session'
import { EmptyState } from '@/components/data/EmptyState'
import { ErrorState } from '@/components/data/ErrorState'
import { PageSkeleton } from '@/components/data/Skeletons'
import { FormError, FormField, SubmitButton, useApplyApiError } from '@/components/forms/FormField'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { must } from '@/lib/pagination'
import { toast } from '@/lib/toast'

export function OrganizationSettings() {
  const { t } = useLingui()
  const { org, can } = useSession()
  const qc = useQueryClient()
  const orgId = org?.id ?? ''
  const canManage = can('org.manage')
  const applyError = useApplyApiError()
  const [saved, setSaved] = useState(0)

  const query = useQuery({
    queryKey: ['tenancy.getOrg', orgId],
    enabled: can('org.read') || canManage,
    queryFn: async () =>
      must(await apiDraft.GET('/v1/orgs/{org_id}', { params: { path: { org_id: orgId } } })),
  })
  const schema = useMemo(
    () =>
      z.object({
        name: z
          .string()
          .trim()
          .min(2, t`Enter the organization name`),
        default_locale: z.string().min(2),
      }),
    [t],
  )
  type Values = z.infer<typeof schema>
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    values: query.data
      ? { name: query.data.name, default_locale: query.data.default_locale }
      : undefined,
  })
  const save = useMutation({
    mutationFn: async (v: Values) =>
      must(
        await apiDraft.PATCH('/v1/orgs/{org_id}', {
          params: { path: { org_id: orgId }, header: ifMatch(query.data!.version) },
          body: { name: v.name.trim(), default_locale: v.default_locale },
        }),
      ),
    onSuccess: async () => {
      setSaved((n) => n + 1)
      await qc.invalidateQueries({ queryKey: ['tenancy.getOrg'] })
      await qc.invalidateQueries({ queryKey: ['me'] })
      toast.info(t`Organization saved.`)
    },
    onError: (e) => applyError(form, e),
  })

  if (!can('org.read') && !canManage) {
    return (
      <EmptyState
        icon={LockIcon}
        title={<Trans>You do not have access to this</Trans>}
        description={<Trans>Ask your manager if you think you should.</Trans>}
      />
    )
  }
  if (query.isPending) return <PageSkeleton />
  if (query.isError) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />
  const errors = form.formState.errors
  return (
    <Card className="max-w-xl" key={saved}>
      <CardHeader>
        <CardTitle>
          <Trans>Organization</Trans>
        </CardTitle>
        <CardDescription>
          <Trans>
            Timezone {query.data.timezone}, country {query.data.country_code}. Site settings live
            under{' '}
            <Link to="/orgs/$orgId/settings/site" params={{ orgId }} className="underline">
              Site
            </Link>
            .
          </Trans>
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form
          className="flex flex-col gap-4"
          noValidate
          onSubmit={(e) => void form.handleSubmit((v) => save.mutate(v))(e)}
        >
          <FormField id="org-name" label={<Trans>Name</Trans>} error={errors.name?.message}>
            <Input id="org-name" disabled={!canManage} {...form.register('name')} />
          </FormField>
          <FormField id="org-locale" label={<Trans>Default language</Trans>}>
            <Select id="org-locale" disabled={!canManage} {...form.register('default_locale')}>
              <option value="tr">Türkçe</option>
              <option value="en">English</option>
            </Select>
          </FormField>
          <FormError message={errors.root?.message} />
          {canManage ? (
            <SubmitButton busy={save.isPending} className="w-fit">
              <Trans>Save</Trans>
            </SubmitButton>
          ) : null}
        </form>
      </CardContent>
    </Card>
  )
}
