import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Trans, useLingui } from '@lingui/react/macro'
import { apiDraft, ifMatch, type TicketCategory } from '@/api/draft'
import { useSession } from '@/app/session'
import { EmptyState } from '@/components/data/EmptyState'
import { ErrorState } from '@/components/data/ErrorState'
import { PageSkeleton } from '@/components/data/Skeletons'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Select } from '@/components/ui/select'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { FormError } from '@/components/forms/FormField'
import { SitePicker, useSite } from '@/features/dashboard/site'
import { useIdempotencyKey, withIdempotencyKey } from '@/lib/idempotency'
import { must } from '@/lib/pagination'
import { toast } from '@/lib/toast'
import { useApplyErrorMessage } from './errors'
import { useCategories, useWorkflow } from './queries'
import { WorkflowEditor } from './WorkflowEditor'
import { LockIcon } from 'lucide-react'

type Role = 'site_manager' | 'staff' | 'board_member'

function CategoryRow({ orgId, category }: { orgId: string; category: TicketCategory }) {
  const { t } = useLingui()
  const qc = useQueryClient()
  const describe = useApplyErrorMessage()
  const [name, setName] = useState(category.name)
  const [visibility, setVisibility] = useState(category.default_visibility)
  const [role, setRole] = useState<Role | ''>(category.default_assignee_role ?? '')
  const [archived, setArchived] = useState(category.archived)
  const [error, setError] = useState<string>()
  const dirty =
    name !== category.name ||
    visibility !== category.default_visibility ||
    role !== (category.default_assignee_role ?? '') ||
    archived !== category.archived
  const save = useMutation({
    mutationFn: async () =>
      must(
        await apiDraft.PATCH('/v1/orgs/{org_id}/ticket-categories/{category_id}', {
          params: {
            path: { org_id: orgId, category_id: category.id },
            header: ifMatch(category.version),
          },
          body: {
            name: name.trim(),
            default_visibility: visibility,
            default_assignee_role: role || null,
            archived,
          },
        }),
      ),
    onSuccess: async () => {
      setError(undefined)
      await qc.invalidateQueries({ queryKey: ['operations.listTicketCategories'] })
      toast.info(t`Category saved.`)
    },
    onError: (e) => setError(describe(e)),
  })
  return (
    <li className="flex flex-col gap-2 rounded-md border p-3">
      <div className="flex flex-wrap items-center gap-2">
        <Input
          className="min-w-40 flex-1"
          aria-label={t`Category name`}
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
        <Select
          className="w-52"
          aria-label={t`Default visibility of ${category.name}`}
          value={visibility}
          onChange={(e) => setVisibility(e.target.value as 'private' | 'common')}
        >
          <option value="private">{t`Private by default`}</option>
          <option value="common">{t`Common by default`}</option>
        </Select>
        <Select
          className="w-52"
          aria-label={t`Default assignee role of ${category.name}`}
          value={role}
          onChange={(e) => setRole(e.target.value as Role | '')}
        >
          <option value="">{t`No default assignee`}</option>
          <option value="site_manager">{t`Site manager`}</option>
          <option value="staff">{t`Staff`}</option>
          <option value="board_member">{t`Board member`}</option>
        </Select>
        <Label className="gap-2">
          <Checkbox checked={archived} onChange={(e) => setArchived(e.target.checked)} />
          <Trans>Archived</Trans>
        </Label>
        <Button
          type="button"
          disabled={!dirty || !name.trim() || save.isPending}
          onClick={() => save.mutate()}
        >
          <Trans>Save</Trans>
        </Button>
      </div>
      <FormError message={error} />
    </li>
  )
}

function NewCategory({ orgId, siteId }: { orgId: string; siteId: string }) {
  const { t } = useLingui()
  const qc = useQueryClient()
  const idem = useIdempotencyKey()
  const describe = useApplyErrorMessage()
  const [name, setName] = useState('')
  const [visibility, setVisibility] = useState<'private' | 'common'>('private')
  const [error, setError] = useState<string>()
  const create = useMutation({
    mutationFn: async () =>
      must(
        await apiDraft.POST('/v1/orgs/{org_id}/ticket-categories', {
          params: { path: { org_id: orgId } },
          body: { site_id: siteId, name: name.trim(), default_visibility: visibility },
          ...withIdempotencyKey(idem.key),
        }),
      ),
    onSuccess: async () => {
      setName('')
      setError(undefined)
      idem.renew()
      await qc.invalidateQueries({ queryKey: ['operations.listTicketCategories'] })
    },
    onError: (e) => setError(describe(e)),
  })
  return (
    <form
      className="flex flex-wrap items-end gap-2"
      onSubmit={(e) => {
        e.preventDefault()
        if (name.trim()) create.mutate()
      }}
    >
      <div className="flex min-w-40 flex-1 flex-col gap-2">
        <Label htmlFor="new-category">
          <Trans>New category</Trans>
        </Label>
        <Input id="new-category" value={name} onChange={(e) => setName(e.target.value)} />
      </div>
      <Select
        className="w-52"
        aria-label={t`Default visibility of the new category`}
        value={visibility}
        onChange={(e) => setVisibility(e.target.value as 'private' | 'common')}
      >
        <option value="private">{t`Private by default`}</option>
        <option value="common">{t`Common by default`}</option>
      </Select>
      <Button type="submit" disabled={!name.trim() || create.isPending}>
        <Trans>Add category</Trans>
      </Button>
      <FormError message={error} />
    </form>
  )
}

/** Per-site ticket configuration (O1): workflow editor and categories. */
export function TicketSettingsPage() {
  const { org, can } = useSession()
  const { site } = useSite()
  const orgId = org?.id ?? ''
  const workflow = useWorkflow(orgId, site?.id)
  const categories = useCategories(orgId, site?.id, undefined)

  if (!can('tickets.manage')) {
    return (
      <EmptyState
        icon={LockIcon}
        title={<Trans>You do not have access to this</Trans>}
        description={<Trans>Ask your manager if you think you should.</Trans>}
      />
    )
  }
  return (
    <div className="flex max-w-3xl flex-col gap-6">
      <div className="flex items-center gap-2">
        <h1 className="me-auto text-2xl font-semibold">
          <Trans>Ticket settings</Trans>
        </h1>
        <SitePicker />
      </div>
      <Card>
        <CardHeader>
          <CardTitle>
            <Trans>Workflow</Trans>
          </CardTitle>
          <CardDescription>
            <Trans>The statuses a ticket moves through at this site, in order.</Trans>
          </CardDescription>
        </CardHeader>
        <CardContent>
          {workflow.isPending ? (
            <PageSkeleton />
          ) : workflow.isError ? (
            <ErrorState error={workflow.error} onRetry={() => void workflow.refetch()} />
          ) : (
            <WorkflowEditor
              key={workflow.data.version}
              orgId={orgId}
              siteId={workflow.data.site_id}
              workflow={workflow.data}
            />
          )}
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>
            <Trans>Categories</Trans>
          </CardTitle>
          <CardDescription>
            <Trans>
              The default visibility and assignee role apply when a ticket is opened in the
              category.
            </Trans>
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          {categories.isPending ? (
            <PageSkeleton />
          ) : categories.isError ? (
            <ErrorState error={categories.error} onRetry={() => void categories.refetch()} />
          ) : (
            <ul className="flex flex-col gap-2">
              {categories.data.map((c) => (
                <CategoryRow key={`${c.id}-${c.version}`} orgId={orgId} category={c} />
              ))}
            </ul>
          )}
          {site ? <NewCategory orgId={orgId} siteId={site.id} /> : null}
        </CardContent>
      </Card>
    </div>
  )
}
