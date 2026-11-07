import { useMemo, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { PlusIcon, WrenchIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import { draftApi, ifMatch, type Schema } from '@/api/draft'
import { runWithReauth } from '@/app/reauth'
import { useSession } from '@/app/session'
import { useIdempotencyKey, withIdempotencyKey } from '@/lib/idempotency'
import { DataTable, ListEmpty, useCursorPages } from '@/components/data/DataTable'
import { Field, FormError, SubmitButton, useApplyFormError } from '@/components/forms/FormBits'
import { PageHeader } from '@/components/layout/PageHeader'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { PeopleNav } from './PeopleNav'
import { personQueries } from './queries'

type Vendor = Schema<'Vendor'>

/** Vendor directory: payees and ticket assignees (labels only; no accounts). */
export function VendorsPage({ search }: { search: { q?: string; archived?: boolean } }) {
  const { org, can } = useSession()
  const { t } = useLingui()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const pages = useCursorPages()
  const orgId = org?.id ?? ''
  const canEdit = can('vendors.manage')
  const [editing, setEditing] = useState<Vendor | 'new' | null>(null)
  const query = useQuery({
    ...personQueries.vendors(orgId, {
      q: search.q,
      archived: search.archived ?? false,
      cursor: pages.cursor,
    }),
    enabled: !!org,
  })
  const archive = useMutation({
    mutationFn: (v: Vendor) =>
      draftApi.PATCH('/v1/orgs/{org_id}/vendors/{vendor_id}', {
        params: { path: { org_id: orgId, vendor_id: v.id }, header: ifMatch(v.version) },
        body: { archived: !v.archived },
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['people.listVendors', orgId] }),
  })

  const columns: ColumnDef<Vendor, unknown>[] = [
    {
      header: t`Name`,
      cell: ({ row }) => <span className="font-medium">{row.original.name}</span>,
    },
    { header: t`Tax id`, cell: ({ row }) => row.original.tax_id ?? '–' },
    {
      header: t`Phone`,
      cell: ({ row }) => <span dir="ltr">{row.original.contact.phone ?? '–'}</span>,
    },
    { header: t`Email`, cell: ({ row }) => row.original.contact.email ?? '–' },
    {
      header: t`Actions`,
      cell: ({ row }) => (
        <span className="flex items-center gap-1">
          {row.original.archived ? (
            <Badge variant="secondary">
              <Trans>Archived</Trans>
            </Badge>
          ) : null}
          {canEdit ? (
            <>
              <Button size="sm" variant="ghost" onClick={() => setEditing(row.original)}>
                <Trans>Edit</Trans>
              </Button>
              <Button size="sm" variant="ghost" onClick={() => archive.mutate(row.original)}>
                {row.original.archived ? <Trans>Restore</Trans> : <Trans>Archive</Trans>}
              </Button>
            </>
          ) : null}
        </span>
      ),
    },
  ]
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={<Trans>Vendors</Trans>}
        actions={
          canEdit ? (
            <Button onClick={() => setEditing('new')}>
              <PlusIcon aria-hidden="true" />
              <Trans>Add vendor</Trans>
            </Button>
          ) : null
        }
      />
      <PeopleNav />
      <div className="flex flex-wrap items-center gap-3">
        <Input
          type="search"
          className="w-56"
          aria-label={t`Search vendors`}
          placeholder={t`Search vendors`}
          defaultValue={search.q ?? ''}
          onChange={(e) => {
            pages.reset()
            void navigate({
              to: '.',
              search: ((p: object) => ({ ...p, q: e.target.value || undefined })) as never,
              replace: true,
            })
          }}
        />
        <label className="flex items-center gap-2 text-sm">
          <Checkbox
            checked={!!search.archived}
            onChange={(e) =>
              void navigate({
                to: '.',
                search: ((p: object) => ({
                  ...p,
                  archived: e.target.checked || undefined,
                })) as never,
                replace: true,
              })
            }
          />
          <Trans>Show archived</Trans>
        </label>
      </div>
      <DataTable
        caption={t`Vendors`}
        columns={columns}
        data={query.data?.items}
        isLoading={query.isLoading}
        error={query.error}
        onRetry={() => void query.refetch()}
        getRowId={(v) => v.id}
        pager={{
          page: pages.page,
          hasPrev: pages.hasPrev,
          hasNext: !!query.data?.next_cursor,
          onPrev: pages.prev,
          onNext: () => query.data?.next_cursor && pages.next(query.data.next_cursor),
        }}
        empty={
          <ListEmpty
            filtered={!!search.q}
            icon={WrenchIcon}
            title={<Trans>No vendors yet</Trans>}
            description={
              <Trans>
                Add the lift service, the plumber, the cleaner: people you pay or assign requests
                to.
              </Trans>
            }
          />
        }
      />
      {editing ? (
        <VendorDialog
          orgId={orgId}
          vendor={editing === 'new' ? undefined : editing}
          onClose={() => setEditing(null)}
        />
      ) : null}
    </div>
  )
}

function VendorDialog({
  orgId,
  vendor,
  onClose,
}: {
  orgId: string
  vendor?: Vendor
  onClose: () => void
}) {
  const { t } = useLingui()
  const queryClient = useQueryClient()
  const idem = useIdempotencyKey()
  const applyError = useApplyFormError()
  const schema = useMemo(
    () =>
      z.object({
        name: z
          .string()
          .trim()
          .min(1, t`Enter the vendor name`),
        tax_id: z.string().trim(),
        phone: z
          .string()
          .trim()
          .refine((v) => v === '' || /^\+?[\d\s()-]{7,20}$/.test(v), t`Enter a valid phone number`),
        email: z
          .string()
          .trim()
          .refine(
            (v) => v === '' || /^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(v),
            t`Enter a valid email address`,
          ),
      }),
    [t],
  )
  const form = useForm({
    resolver: zodResolver(schema),
    defaultValues: {
      name: vendor?.name ?? '',
      tax_id: vendor?.tax_id ?? '',
      phone: vendor?.contact.phone ?? '',
      email: vendor?.contact.email ?? '',
    },
  })
  const save = useMutation({
    mutationFn: (v: z.infer<typeof schema>) => {
      const body = {
        name: v.name,
        tax_id: v.tax_id || null,
        contact: { ...(vendor?.contact ?? {}), phone: v.phone || null, email: v.email || null },
      }
      return runWithReauth(() =>
        vendor
          ? draftApi.PATCH('/v1/orgs/{org_id}/vendors/{vendor_id}', {
              params: {
                path: { org_id: orgId, vendor_id: vendor.id },
                header: ifMatch(vendor.version),
              },
              body,
            })
          : draftApi.POST('/v1/orgs/{org_id}/vendors', {
              params: { path: { org_id: orgId } },
              body,
              ...withIdempotencyKey(idem.key),
            }),
      )
    },
    onSuccess: async () => {
      idem.renew()
      await queryClient.invalidateQueries({ queryKey: ['people.listVendors', orgId] })
      onClose()
    },
    onError: (e) => applyError(form, e, { 'contact.phone': 'phone', 'contact.email': 'email' }),
  })
  const { errors } = form.formState
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <form
          noValidate
          className="grid gap-4"
          onSubmit={form.handleSubmit((v) => save.mutateAsync(v).catch(() => undefined))}
        >
          <DialogHeader>
            <DialogTitle>
              {vendor ? <Trans>Edit vendor</Trans> : <Trans>Add vendor</Trans>}
            </DialogTitle>
          </DialogHeader>
          <Field id="v-name" label={<Trans>Name</Trans>} error={errors.name?.message}>
            <Input id="v-name" aria-invalid={!!errors.name} {...form.register('name')} />
          </Field>
          <Field id="v-tax" label={<Trans>Tax id</Trans>} error={errors.tax_id?.message}>
            <Input id="v-tax" dir="ltr" {...form.register('tax_id')} />
          </Field>
          <Field id="v-phone" label={<Trans>Phone</Trans>} error={errors.phone?.message}>
            <Input
              id="v-phone"
              type="tel"
              dir="ltr"
              aria-invalid={!!errors.phone}
              {...form.register('phone')}
            />
          </Field>
          <Field id="v-email" label={<Trans>Email</Trans>} error={errors.email?.message}>
            <Input
              id="v-email"
              type="email"
              dir="ltr"
              aria-invalid={!!errors.email}
              {...form.register('email')}
            />
          </Field>
          <FormError message={errors.root?.message} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              <Trans>Cancel</Trans>
            </Button>
            <SubmitButton busy={save.isPending}>
              <Trans>Save vendor</Trans>
            </SubmitButton>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
