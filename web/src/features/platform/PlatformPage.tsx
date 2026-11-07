import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Trans, useLingui } from '@lingui/react/macro'
import { PlusIcon, ServerIcon } from 'lucide-react'
import { apiDraft, ifMatch, type PlatformOrg } from '@/api/draft'
import { useSession } from '@/app/session'
import { EmptyState } from '@/components/data/EmptyState'
import { ErrorState } from '@/components/data/ErrorState'
import { PageSkeleton, TableSkeleton } from '@/components/data/Skeletons'
import { FormError, FormField, SubmitButton, useApplyApiError } from '@/components/forms/FormField'
import { Badge } from '@/components/ui/badge'
import { Button, buttonVariants } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/select'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select } from '@/components/ui/select'
import { useIdempotencyKey, withIdempotencyKey } from '@/lib/idempotency'
import { LoadMore, must, useCursorQuery } from '@/lib/pagination'
import { toast } from '@/lib/toast'
import { cn } from '@/lib/utils'
import { useLocale } from '@/app/locale'

function StatusBadge({ status }: { status: string }) {
  const variant =
    status === 'ok' || status === 'active' || status === 'completed' || status === 'running'
      ? 'success'
      : status === 'degraded' || status === 'suspended' || status === 'retryable'
        ? 'warning'
        : status === 'down' || status === 'discarded' || status === 'failed'
          ? 'destructive'
          : 'outline'
  return <Badge variant={variant}>{status}</Badge>
}

function CreateOrgDialog({ onClose }: { onClose: () => void }) {
  const { t } = useLingui()
  const qc = useQueryClient()
  const idem = useIdempotencyKey()
  const applyError = useApplyApiError()
  const schema = useMemo(
    () =>
      z.object({
        slug: z
          .string()
          .trim()
          .regex(/^[a-z0-9-]{2,40}$/, t`Use 2 to 40 lowercase letters, digits or dashes`),
        name: z
          .string()
          .trim()
          .min(2, t`Enter the organization name`),
        admin_email: z.union([z.literal(''), z.email(t`Enter a valid email address`)]),
        default_locale: z.enum(['tr', 'en']),
      }),
    [t],
  )
  type Values = z.infer<typeof schema>
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { slug: '', name: '', admin_email: '', default_locale: 'tr' },
  })
  const create = useMutation({
    mutationFn: async (v: Values) =>
      must(
        await apiDraft.POST('/v1/platform/orgs', {
          body: {
            slug: v.slug,
            name: v.name.trim(),
            default_locale: v.default_locale,
            admin_email: v.admin_email || undefined,
          },
          ...withIdempotencyKey(idem.key),
        }),
      ),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ['platform.listOrgs'] })
      toast.info(t`Organization created.`)
      onClose()
    },
    onError: (e) => applyError(form, e),
  })
  const errors = form.formState.errors
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            <Trans>New organization</Trans>
          </DialogTitle>
          <DialogDescription>
            <Trans>The first administrator is invited by email.</Trans>
          </DialogDescription>
        </DialogHeader>
        <form
          className="flex flex-col gap-3"
          noValidate
          onSubmit={(e) => void form.handleSubmit((v) => create.mutate(v))(e)}
        >
          <FormField
            id="org-slug"
            label={<Trans>Short name (slug)</Trans>}
            error={errors.slug?.message}
          >
            <Input id="org-slug" {...form.register('slug')} />
          </FormField>
          <FormField id="org-name" label={<Trans>Name</Trans>} error={errors.name?.message}>
            <Input id="org-name" {...form.register('name')} />
          </FormField>
          <FormField
            id="org-admin"
            label={<Trans>Administrator email (optional)</Trans>}
            error={errors.admin_email?.message}
          >
            <Input id="org-admin" type="email" {...form.register('admin_email')} />
          </FormField>
          <FormField id="org-locale" label={<Trans>Default language</Trans>}>
            <Select id="org-locale" {...form.register('default_locale')}>
              <option value="tr">Türkçe</option>
              <option value="en">English</option>
            </Select>
          </FormField>
          <FormError message={errors.root?.message} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              <Trans>Cancel</Trans>
            </Button>
            <SubmitButton busy={create.isPending}>
              <Trans>Create</Trans>
            </SubmitButton>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function OrgRow({ org }: { org: PlatformOrg }) {
  const { t } = useLingui()
  const qc = useQueryClient()
  const toggle = useMutation({
    mutationFn: async () =>
      must(
        await apiDraft.PATCH('/v1/platform/orgs/{org_id}', {
          params: { path: { org_id: org.id }, header: ifMatch(org.version) },
          body: { status: org.status === 'active' ? 'suspended' : 'active' },
        }),
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['platform.listOrgs'] }),
    onError: () => toast.error(t`Could not change the organization.`),
  })
  return (
    <tr className="border-b">
      <td className="py-2 font-medium">{org.name}</td>
      <td className="py-2 font-mono text-xs">{org.slug}</td>
      <td className="py-2">
        <StatusBadge status={org.status} />
      </td>
      <td className="py-2 text-end">{org.site_count}</td>
      <td className="py-2 text-end">
        {org.status !== 'archived' ? (
          <Button
            variant="ghost"
            size="sm"
            disabled={toggle.isPending}
            onClick={() => toggle.mutate()}
          >
            {org.status === 'active' ? <Trans>Suspend</Trans> : <Trans>Reactivate</Trans>}
          </Button>
        ) : null}
      </td>
    </tr>
  )
}

function OrgsTab({ canManage }: { canManage: boolean }) {
  const [creating, setCreating] = useState(false)
  const list = useCursorQuery<PlatformOrg>(['platform.listOrgs'], async (cursor) =>
    must(await apiDraft.GET('/v1/platform/orgs', { params: { query: { limit: 50, cursor } } })),
  )
  return (
    <div className="flex flex-col gap-4">
      {canManage ? (
        <Button className="w-fit" onClick={() => setCreating(true)}>
          <PlusIcon aria-hidden="true" />
          <Trans>New organization</Trans>
        </Button>
      ) : null}
      {list.isPending ? (
        <TableSkeleton rows={3} columns={4} />
      ) : list.isError ? (
        <ErrorState error={list.error} onRetry={() => void list.refetch()} />
      ) : list.items.length === 0 ? (
        <EmptyState icon={ServerIcon} title={<Trans>No organizations yet</Trans>} />
      ) : (
        <>
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b">
                <th scope="col" className="py-2 text-start font-medium">
                  <Trans>Name</Trans>
                </th>
                <th scope="col" className="py-2 text-start font-medium">
                  <Trans>Slug</Trans>
                </th>
                <th scope="col" className="py-2 text-start font-medium">
                  <Trans>Status</Trans>
                </th>
                <th scope="col" className="py-2 text-end font-medium">
                  <Trans>Sites</Trans>
                </th>
                <th scope="col" className="py-2">
                  <span className="sr-only">
                    <Trans>Actions</Trans>
                  </span>
                </th>
              </tr>
            </thead>
            <tbody>
              {list.items.map((o) => (
                <OrgRow key={o.id} org={o} />
              ))}
            </tbody>
          </table>
          <LoadMore query={list} />
        </>
      )}
      {creating ? <CreateOrgDialog onClose={() => setCreating(false)} /> : null}
    </div>
  )
}

function HealthTab() {
  const { intl } = useLocale()
  const health = useQuery({
    queryKey: ['platform.getHealth'],
    refetchInterval: 30_000,
    queryFn: async () => must(await apiDraft.GET('/v1/platform/health')),
  })
  const backup = useQuery({
    queryKey: ['platform.getBackupStatus'],
    queryFn: async () => must(await apiDraft.GET('/v1/platform/backup-status')),
  })
  const fmt = (iso: string | null | undefined) =>
    iso
      ? new Intl.DateTimeFormat(intl, { dateStyle: 'medium', timeStyle: 'short' }).format(
          new Date(iso),
        )
      : '—'
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Trans>Health</Trans>
            {health.data ? <StatusBadge status={health.data.status} /> : null}
          </CardTitle>
        </CardHeader>
        <CardContent>
          {health.isPending ? (
            <PageSkeleton />
          ) : health.isError ? (
            <ErrorState error={health.error} onRetry={() => void health.refetch()} />
          ) : (
            <div className="flex flex-col gap-3 text-sm">
              <p>
                <Trans>
                  Server {health.data.server_version}. Jobs queued: {health.data.jobs_queued},
                  failed: {health.data.jobs_failed}.
                </Trans>
              </p>
              <ul className="flex flex-col gap-2">
                {health.data.checks.map((c) => (
                  <li key={c.name} className="flex items-center gap-2">
                    <StatusBadge status={c.status} />
                    <span className="font-medium">{c.name}</span>
                    <span className="text-xs text-muted-foreground">
                      {c.detail ?? ''}
                      {c.latency_ms != null ? ` ${c.latency_ms} ms` : ''}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          )}
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>
            <Trans>Backups</Trans>
          </CardTitle>
        </CardHeader>
        <CardContent>
          {backup.isPending ? (
            <PageSkeleton />
          ) : backup.isError ? (
            <ErrorState error={backup.error} onRetry={() => void backup.refetch()} />
          ) : (
            <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
              <dt className="text-muted-foreground">
                <Trans>Last backup</Trans>
              </dt>
              <dd className="flex items-center gap-2">
                {fmt(backup.data.last_backup_at)}
                <StatusBadge status={backup.data.last_backup_status} />
              </dd>
              <dt className="text-muted-foreground">
                <Trans>Size</Trans>
              </dt>
              <dd>
                {backup.data.last_backup_size_bytes != null
                  ? `${(backup.data.last_backup_size_bytes / 1024 / 1024).toFixed(0)} MB`
                  : '—'}
              </dd>
              <dt className="text-muted-foreground">
                <Trans>Next backup</Trans>
              </dt>
              <dd>{fmt(backup.data.next_backup_at)}</dd>
            </dl>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

function JobsTab() {
  const { intl } = useLocale()
  const list = useCursorQuery(['platform.listJobs'], async (cursor) =>
    must(await apiDraft.GET('/v1/platform/jobs', { params: { query: { limit: 50, cursor } } })),
  )
  if (list.isPending) return <TableSkeleton rows={4} columns={4} />
  if (list.isError) return <ErrorState error={list.error} onRetry={() => void list.refetch()} />
  if (list.items.length === 0) {
    return <EmptyState icon={ServerIcon} title={<Trans>No jobs</Trans>} />
  }
  return (
    <>
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b">
            <th scope="col" className="py-2 text-start font-medium">
              <Trans>Job</Trans>
            </th>
            <th scope="col" className="py-2 text-start font-medium">
              <Trans>State</Trans>
            </th>
            <th scope="col" className="py-2 text-end font-medium">
              <Trans>Attempts</Trans>
            </th>
            <th scope="col" className="py-2 text-start font-medium">
              <Trans>Created</Trans>
            </th>
            <th scope="col" className="py-2 text-start font-medium">
              <Trans>Error</Trans>
            </th>
          </tr>
        </thead>
        <tbody>
          {list.items.map((j) => (
            <tr key={j.id} className="border-b">
              <td className="py-2 font-mono text-xs">{j.kind}</td>
              <td className="py-2">
                <StatusBadge status={j.state} />
              </td>
              <td className="py-2 text-end">{j.attempts}</td>
              <td className="py-2 text-xs">
                {new Intl.DateTimeFormat(intl, { dateStyle: 'short', timeStyle: 'short' }).format(
                  new Date(j.created_at),
                )}
              </td>
              <td className="py-2 text-xs text-destructive">{j.error ?? ''}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <LoadMore query={list} />
    </>
  )
}

function SettingsTab({ canManage }: { canManage: boolean }) {
  const { t } = useLingui()
  const qc = useQueryClient()
  const query = useQuery({
    queryKey: ['platform.getSettings'],
    queryFn: async () => must(await apiDraft.GET('/v1/platform/settings')),
  })
  const [name, setName] = useState<string>()
  const save = useMutation({
    mutationFn: async (patch: { instance_name?: string; registration_open?: boolean }) =>
      must(
        await apiDraft.PATCH('/v1/platform/settings', {
          params: { header: ifMatch(query.data!.version) },
          body: patch,
        }),
      ),
    onSuccess: async () => {
      setName(undefined)
      await qc.invalidateQueries({ queryKey: ['platform.getSettings'] })
      toast.info(t`Settings saved.`)
    },
    onError: () => toast.error(t`Could not save the settings.`),
  })
  if (query.isPending) return <PageSkeleton />
  if (query.isError) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />
  const s = query.data
  return (
    <Card className="max-w-xl">
      <CardContent className="flex flex-col gap-4 pt-6">
        <form
          className="flex flex-col gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            if (name?.trim()) save.mutate({ instance_name: name.trim() })
          }}
        >
          <Label htmlFor="instance-name">
            <Trans>Instance name</Trans>
          </Label>
          <div className="flex gap-2">
            <Input
              id="instance-name"
              disabled={!canManage}
              value={name ?? s.instance_name}
              onChange={(e) => setName(e.target.value)}
            />
            {canManage ? (
              <Button type="submit" disabled={name === undefined || save.isPending}>
                <Trans>Save</Trans>
              </Button>
            ) : null}
          </div>
        </form>
        <Label className="gap-2 font-normal">
          <Checkbox
            checked={s.registration_open}
            disabled={!canManage || save.isPending}
            onChange={(e) => save.mutate({ registration_open: e.target.checked })}
          />
          <Trans>Open registration</Trans>
        </Label>
        <p className="text-sm text-muted-foreground">
          <Trans>
            Mail: {s.mail_configured ? t`configured` : t`not configured`}. SMS:{' '}
            {s.sms_configured ? t`configured` : t`not configured`}.
          </Trans>
        </p>
      </CardContent>
    </Card>
  )
}

type Tab = 'orgs' | 'health' | 'jobs' | 'settings'

/** Platform administration: organizations, health, jobs, instance settings, backup status. */
export function PlatformPage() {
  const { t } = useLingui()
  const { me } = useSession()
  const [tab, setTab] = useState<Tab>('orgs')
  // `platform.manage` is held by platform admins; no platform permission appears in /v1/me, so the
  // route guard (is_platform_admin) is the check and the server enforces the rest.
  const canManage = !!me?.user.is_platform_admin
  const tabs: [Tab, string][] = [
    ['orgs', t`Organizations`],
    ['health', t`Health`],
    ['jobs', t`Jobs`],
    ['settings', t`Instance settings`],
  ]
  return (
    <div className="mx-auto flex min-h-dvh max-w-5xl flex-col gap-4 p-4 md:p-6">
      <div className="flex items-center gap-2">
        <h1 className="me-auto text-2xl font-semibold">
          <Trans>Platform administration</Trans>
        </h1>
        <Link to="/" className={buttonVariants({ variant: 'outline' })}>
          <Trans>Go home</Trans>
        </Link>
      </div>
      <div
        role="tablist"
        aria-label={t`Platform sections`}
        className="flex flex-wrap gap-1 border-b"
      >
        {tabs.map(([id, label]) => (
          <button
            key={id}
            role="tab"
            type="button"
            aria-selected={tab === id}
            onClick={() => setTab(id)}
            className={cn(
              '-mb-px border-b-2 px-3 py-2 text-sm font-medium outline-none focus-visible:ring-[3px] focus-visible:ring-ring',
              tab === id ? 'border-primary' : 'border-transparent text-muted-foreground',
            )}
          >
            {label}
          </button>
        ))}
      </div>
      {tab === 'orgs' ? <OrgsTab canManage={canManage} /> : null}
      {tab === 'health' ? <HealthTab /> : null}
      {tab === 'jobs' ? <JobsTab /> : null}
      {tab === 'settings' ? <SettingsTab canManage={canManage} /> : null}
    </div>
  )
}
