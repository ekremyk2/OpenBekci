import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { SendIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import { draftApi, type Schema } from '@/api/draft'
import { runWithReauth } from '@/app/reauth'
import { useSession } from '@/app/session'
import { useIdempotencyKey, withIdempotencyKey } from '@/lib/idempotency'
import { DataTable, ListEmpty, useCursorPages } from '@/components/data/DataTable'
import { SiteDate } from '@/components/data/SiteDate'
import { zoneOfSite } from '@/lib/dates'
import { normalizeDestination } from '@/lib/contact'
import { PersonPicker, type PickerOption } from '@/components/forms/EntityPicker'
import { Field, FormError, SubmitButton, useApplyFormError } from '@/components/forms/FormBits'
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
import { Select } from '@/components/ui/select'
import { inviteStatusLabel } from './labels'
import { personQueries } from './queries'

/**
 * Invitations (S4): who was invited by SMS or email and whether they joined yet. With `personId`
 * the list is that person's and the person picker is fixed.
 */
export function InvitationsPanel({ orgId, personId }: { orgId: string; personId?: string }) {
  const { t, i18n } = useLingui()
  const { org, can } = useSession()
  const queryClient = useQueryClient()
  const pages = useCursorPages()
  const [open, setOpen] = useState(false)
  const query = useQuery(personQueries.invitations(orgId, { personId, cursor: pages.cursor }))
  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: ['people.listInvitations', orgId] })
  const resend = useMutation({
    mutationFn: (id: string) =>
      runWithReauth((key) =>
        draftApi.POST('/v1/orgs/{org_id}/invitations/{invitation_id}:resend', {
          params: { path: { org_id: orgId, invitation_id: id } },
          ...withIdempotencyKey(key),
        }),
      ),
    onSuccess: invalidate,
  })
  const revoke = useMutation({
    mutationFn: (id: string) =>
      draftApi.DELETE('/v1/orgs/{org_id}/invitations/{invitation_id}', {
        params: { path: { org_id: orgId, invitation_id: id } },
      }),
    onSuccess: invalidate,
  })

  const columns: ColumnDef<Schema<'Invitation'>, unknown>[] = [
    { header: t`Sent to`, cell: ({ row }) => <span dir="ltr">{row.original.destination}</span> },
    { header: t`By`, cell: ({ row }) => (row.original.channel === 'sms' ? 'SMS' : t`Email`) },
    {
      header: t`Joined?`,
      cell: ({ row }) => {
        const s = row.original.status
        return (
          <Badge variant={s === 'accepted' ? 'success' : s === 'pending' ? 'info' : 'secondary'}>
            {inviteStatusLabel(s, i18n)}
          </Badge>
        )
      },
    },
    {
      header: t`Expires`,
      cell: ({ row }) => (
        <SiteDate
          value={row.original.expires_at}
          timeZone={zoneOfSite(org?.sites, row.original.site_id)}
        />
      ),
    },
    {
      header: t`Actions`,
      cell: ({ row }) =>
        row.original.status === 'accepted' ? null : (
          <span className="flex gap-1">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => resend.mutate(row.original.id)}
              disabled={resend.isPending}
            >
              <Trans>Resend</Trans>
            </Button>
            {row.original.status === 'pending' ? (
              <Button variant="ghost" size="sm" onClick={() => revoke.mutate(row.original.id)}>
                <Trans>Revoke</Trans>
              </Button>
            ) : null}
          </span>
        ),
    },
  ]

  return (
    <div className="flex flex-col gap-3">
      {can('invitations.manage') ? (
        <div>
          <Button onClick={() => setOpen(true)}>
            <SendIcon aria-hidden="true" />
            <Trans>Invite</Trans>
          </Button>
        </div>
      ) : null}
      <DataTable
        caption={t`Invitations`}
        columns={columns}
        data={query.data?.items}
        isLoading={query.isLoading}
        error={query.error}
        onRetry={() => void query.refetch()}
        getRowId={(i) => i.id}
        pager={{
          page: pages.page,
          hasPrev: pages.hasPrev,
          hasNext: !!query.data?.next_cursor,
          onPrev: pages.prev,
          onNext: () => query.data?.next_cursor && pages.next(query.data.next_cursor),
        }}
        empty={
          <ListEmpty
            filtered={false}
            title={<Trans>No invitations yet</Trans>}
            description={
              <Trans>
                Invite a person by SMS or email. They set a password and are linked to their unit.
              </Trans>
            }
          />
        }
      />
      <InviteDialog
        orgId={orgId}
        personId={personId}
        open={open}
        onOpenChange={setOpen}
        onSent={invalidate}
      />
    </div>
  )
}

function InviteDialog({
  orgId,
  personId,
  open,
  onOpenChange,
  onSent,
}: {
  orgId: string
  personId?: string
  open: boolean
  onOpenChange: (o: boolean) => void
  onSent: () => void
}) {
  const { t } = useLingui()
  const { org } = useSession()
  const sites = org?.sites ?? []
  const idem = useIdempotencyKey()
  const applyError = useApplyFormError()
  const [person, setPerson] = useState<PickerOption | null>(null)
  const [personError, setPersonError] = useState<string | undefined>()
  const schema = useMemo(
    () =>
      z
        .object({
          site_id: z.string().min(1),
          channel: z.string(),
          destination: z.string().trim(),
        })
        .refine((v) => v.destination === '' || normalizeDestination(v.channel, v.destination), {
          path: ['destination'],
          message: t`Enter a phone number in international format (+90 555 111 22 33) or a valid email address`,
        }),
    [t],
  )
  const form = useForm({
    resolver: zodResolver(schema),
    defaultValues: { site_id: sites[0]?.id ?? '', channel: 'sms', destination: '' },
  })
  const send = useMutation({
    mutationFn: (v: z.infer<typeof schema>) =>
      runWithReauth(() =>
        draftApi.POST('/v1/orgs/{org_id}/invitations', {
          params: { path: { org_id: orgId } },
          body: {
            site_id: v.site_id,
            person_id: personId ?? person!.id,
            channel: v.channel === 'email' ? 'email' : 'sms',
            destination: v.destination
              ? (normalizeDestination(v.channel, v.destination) ?? v.destination)
              : undefined,
          },
          ...withIdempotencyKey(idem.key),
        }),
      ),
    onSuccess: async () => {
      idem.renew()
      form.reset()
      setPerson(null)
      onSent()
      onOpenChange(false)
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
          onSubmit={form.handleSubmit((v) => {
            if (!personId && !person) return setPersonError(t`Choose who to invite`)
            setPersonError(undefined)
            return send.mutateAsync(v).catch(() => undefined)
          })}
        >
          <DialogHeader>
            <DialogTitle>
              <Trans>Invite a person</Trans>
            </DialogTitle>
            <DialogDescription>
              <Trans>They receive a link to set a password and join the site.</Trans>
            </DialogDescription>
          </DialogHeader>
          {personId ? null : (
            <Field id="inv-person" label={<Trans>Person</Trans>} error={personError}>
              <PersonPicker
                id="inv-person"
                orgId={orgId}
                value={person}
                onChange={setPerson}
                invalid={!!personError}
              />
            </Field>
          )}
          {sites.length > 1 ? (
            <Field id="inv-site" label={<Trans>Site</Trans>}>
              <Select id="inv-site" {...form.register('site_id')}>
                {sites.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
              </Select>
            </Field>
          ) : null}
          <Field id="inv-channel" label={<Trans>Send by</Trans>}>
            <Select id="inv-channel" {...form.register('channel')}>
              <option value="sms">SMS</option>
              <option value="email">{t`Email`}</option>
            </Select>
          </Field>
          <Field
            id="inv-dest"
            label={<Trans>Send to (optional)</Trans>}
            error={errors.destination?.message}
            hint={
              <Trans>Leave empty to use the number or address on the person&apos;s record.</Trans>
            }
          >
            <Input id="inv-dest" dir="ltr" {...form.register('destination')} />
          </Field>
          <FormError message={errors.root?.message} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              <Trans>Cancel</Trans>
            </Button>
            <SubmitButton busy={send.isPending}>
              <Trans>Send invitation</Trans>
            </SubmitButton>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
