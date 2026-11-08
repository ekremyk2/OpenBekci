import { useMemo, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Trans, useLingui } from '@lingui/react/macro'
import { PlusIcon } from 'lucide-react'
import { useReauthGuard } from '@/components/ReauthDialog'
import { SiteDate } from '@/components/data/SiteDate'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { useIdempotencyKey } from '@/lib/idempotency'
import { toast } from '@/lib/toast'
import {
  gApi,
  idemHeader,
  unwrap,
  useKeyStore,
  useOrgMutation,
  useOrgQuery,
  type S,
  type SiteCtx,
} from '@/features/meetings/kit/api'
import {
  applyServerErrors,
  ConfirmDialog,
  Field,
  FormRootError,
  QueryBoundary,
  Select,
} from '@/features/meetings/kit/ui'
import { Callout } from '@/features/meetings/kit/widgets'

function NewCredentialDialog({ ctx, onClose }: { ctx: SiteCtx; onClose: () => void }) {
  const { t } = useLingui()
  const idem = useIdempotencyKey()
  const { run, dialog } = useReauthGuard()
  const schema = useMemo(
    () =>
      z
        .object({
          kind: z.enum(['tag', 'pin']),
          value: z
            .string()
            .trim()
            .min(1, t`Enter the value.`),
          scope: z.enum(['all_vehicle', 'all_pedestrian', 'all']),
          valid_to: z.string(),
          max_uses: z.string().refine((s) => s === '' || /^\d+$/.test(s), t`Enter a whole number.`),
        })
        .refine((v) => v.kind !== 'pin' || /^\d{4,8}$/.test(v.value), {
          path: ['value'],
          message: t`A PIN is 4 to 8 digits.`,
        }),
    [t],
  )
  type Values = z.infer<typeof schema>
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { kind: 'tag', value: '', scope: 'all', valid_to: '', max_uses: '' },
  })
  const kind = useWatch({ control: form.control, name: 'kind' })
  const create = useOrgMutation(async (v: Values) =>
    run(() =>
      unwrap(
        gApi.POST('/v1/orgs/{org_id}/credentials', {
          params: { path: { org_id: ctx.orgId }, header: idemHeader(idem.key) },
          body: {
            site_id: ctx.siteId,
            kind: v.kind,
            value: v.value.trim(),
            point_scope: v.scope,
            ...(v.valid_to ? { valid_to: new Date(`${v.valid_to}T23:59:00`).toISOString() } : {}),
            ...(v.max_uses ? { max_uses: Number(v.max_uses) } : {}),
          },
        }),
      ),
    ),
  )
  const err = form.formState.errors
  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : undefined)}>
      <DialogContent>
        <form
          className="flex flex-col gap-4"
          noValidate
          onSubmit={form.handleSubmit(async (v) => {
            try {
              const r = await create.mutateAsync(v)
              if (!r) return
              idem.renew()
              toast.info(t`Credential created.`)
              onClose()
            } catch (e) {
              applyServerErrors(form, e, t`Could not create the credential.`)
            }
          })}
        >
          <DialogHeader>
            <DialogTitle>
              <Trans>New manual credential</Trans>
            </DialogTitle>
            <DialogDescription>
              <Trans>
                Plates and visitor codes are created from vehicles and visitors. Add a tag or a PIN
                here.
              </Trans>
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label={t`Kind`} htmlFor="cr-kind">
              <Select id="cr-kind" {...form.register('kind')}>
                <option value="tag">{t`Tag or card`}</option>
                <option value="pin">{t`PIN`}</option>
              </Select>
            </Field>
            <Field label={t`Opens`} htmlFor="cr-scope">
              <Select id="cr-scope" {...form.register('scope')}>
                <option value="all">{t`All gates and doors`}</option>
                <option value="all_vehicle">{t`Vehicle gates`}</option>
                <option value="all_pedestrian">{t`Pedestrian doors`}</option>
              </Select>
            </Field>
          </div>
          <Field
            label={kind === 'pin' ? t`PIN` : t`Tag number`}
            htmlFor="cr-value"
            error={err.value?.message}
          >
            <Input
              id="cr-value"
              dir="ltr"
              autoComplete="off"
              type={kind === 'pin' ? 'password' : 'text'}
              {...form.register('value')}
            />
          </Field>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label={t`Valid until (optional)`} htmlFor="cr-valid">
              <Input id="cr-valid" type="date" dir="ltr" {...form.register('valid_to')} />
            </Field>
            <Field label={t`Most uses (optional)`} htmlFor="cr-uses" error={err.max_uses?.message}>
              <Input id="cr-uses" inputMode="numeric" dir="ltr" {...form.register('max_uses')} />
            </Field>
          </div>
          <FormRootError message={err.root?.message} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              <Trans>Cancel</Trans>
            </Button>
            <Button type="submit" disabled={create.isPending}>
              <Trans>Create</Trans>
            </Button>
          </DialogFooter>
        </form>
        {dialog}
      </DialogContent>
    </Dialog>
  )
}

/** Credentials: derived ones (plates, visitor codes) are read-only; tags and PINs are manual. */
export function CredentialsTab({ ctx, canManage }: { ctx: SiteCtx; canManage: boolean }) {
  const { t } = useLingui()
  const keys = useKeyStore()
  const [kind, setKind] = useState('')
  const [status, setStatus] = useState('active')
  const [q, setQ] = useState('')
  const [adding, setAdding] = useState(false)
  const [revoking, setRevoking] = useState<S['Credential'] | null>(null)
  const list = useOrgQuery(['credentials', ctx.siteId, kind, status, q], () =>
    unwrap(
      gApi.GET('/v1/orgs/{org_id}/credentials', {
        params: {
          path: { org_id: ctx.orgId },
          query: {
            site_id: ctx.siteId,
            limit: 100,
            ...(kind ? { kind } : {}),
            ...(status ? { status } : {}),
            ...(q.trim() ? { q: q.trim() } : {}),
          },
        },
      }),
    ).then((r) => r.items),
  )
  const revoke = useOrgMutation(async (c: S['Credential']) => {
    await gApi.DELETE('/v1/orgs/{org_id}/credentials/{credential_id}', {
      params: {
        path: { org_id: ctx.orgId, credential_id: c.id },
        header: idemHeader(keys.get(c.id)),
      },
    })
    keys.done(c.id)
  })
  const kinds = { plate: t`Plate`, visitor_qr: t`Visitor code`, tag: t`Tag`, pin: t`PIN` }
  const sources = {
    vehicle: t`From a vehicle`,
    visitor_entry: t`From a visitor`,
    manual: t`Manual`,
  }
  return (
    <div className="flex flex-col gap-4">
      <Callout tone="info">
        <Trans>
          The list is derived from vehicles and visitors. Only tags and PINs are created and revoked
          here.
        </Trans>
      </Callout>
      <div className="flex flex-wrap items-center gap-2">
        <Input
          aria-label={t`Search credentials`}
          placeholder={t`Search`}
          className="max-w-xs"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
        <Select
          aria-label={t`Kind`}
          className="w-auto"
          value={kind}
          onChange={(e) => setKind(e.target.value)}
        >
          <option value="">{t`All kinds`}</option>
          {Object.entries(kinds).map(([k, v]) => (
            <option key={k} value={k}>
              {v}
            </option>
          ))}
        </Select>
        <Select
          aria-label={t`Status`}
          className="w-auto"
          value={status}
          onChange={(e) => setStatus(e.target.value)}
        >
          <option value="">{t`Any status`}</option>
          <option value="active">{t`Active`}</option>
          <option value="suspended">{t`Suspended`}</option>
          <option value="revoked">{t`Revoked`}</option>
          <option value="expired">{t`Expired`}</option>
        </Select>
        {canManage ? (
          <Button className="ms-auto" onClick={() => setAdding(true)}>
            <PlusIcon />
            <Trans>New tag or PIN</Trans>
          </Button>
        ) : null}
      </div>
      <QueryBoundary
        query={list}
        isEmpty={(d) => d.length === 0}
        emptyTitle={t`No credentials found`}
      >
        {(items) => (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>
                  <Trans>Kind</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Value</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Unit</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Source</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Valid until</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Status</Trans>
                </TableHead>
                <TableHead>
                  <span className="sr-only">
                    <Trans>Actions</Trans>
                  </span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((c) => (
                <TableRow key={c.id}>
                  <TableCell>{kinds[c.kind] ?? c.kind}</TableCell>
                  <TableCell dir="ltr" className="font-mono text-sm">
                    {c.kind === 'pin' || c.kind === 'visitor_qr' ? '••••' : c.value}
                  </TableCell>
                  <TableCell>{c.unit_door_number ?? '–'}</TableCell>
                  <TableCell>{sources[c.source_type] ?? c.source_type}</TableCell>
                  <TableCell>
                    {c.valid_to ? (
                      <SiteDate value={c.valid_to} timeZone={ctx.timeZone} kind="datetime" />
                    ) : (
                      '–'
                    )}
                  </TableCell>
                  <TableCell>
                    <Badge variant={c.status === 'active' ? 'success' : 'secondary'}>
                      {c.status}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    {canManage && c.source_type === 'manual' && c.status === 'active' ? (
                      <Button size="sm" variant="outline" onClick={() => setRevoking(c)}>
                        <Trans>Revoke</Trans>
                      </Button>
                    ) : null}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </QueryBoundary>
      {adding ? <NewCredentialDialog ctx={ctx} onClose={() => setAdding(false)} /> : null}
      <ConfirmDialog
        open={revoking !== null}
        onOpenChange={(o) => (!o ? setRevoking(null) : undefined)}
        title={t`Revoke this credential?`}
        description={t`It stops opening gates at once.`}
        confirmLabel={t`Revoke`}
        destructive
        pending={revoke.isPending}
        onConfirm={() =>
          revoking && revoke.mutate(revoking, { onSuccess: () => setRevoking(null) })
        }
      />
    </div>
  )
}
