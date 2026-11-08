import { useMemo } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Trans, useLingui } from '@lingui/react/macro'
import { draftApi } from '@/api/draft'
import { runWithReauth } from '@/app/reauth'
import { useSession } from '@/app/session'
import { normalizeDestination } from '@/lib/contact'
import { useIdempotencyKey, withIdempotencyKey } from '@/lib/idempotency'
import { ErrorState } from '@/components/data/ErrorState'
import { Field, FormError, SubmitButton, useApplyFormError } from '@/components/forms/FormBits'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { inviteStatusLabel } from './labels'

/**
 * Household members of the caller's own unit (S3/S4): invite someone (household occupancy only, no
 * money access), see and revoke the invitations the caller sent, and end a household occupancy.
 * Shown on `/me` to owners and tenants (`invitations.household`).
 */
export function HouseholdPanel() {
  const { org, me, can } = useSession()
  const orgId = org?.id ?? ''
  if (!can('invitations.household')) return null
  // one section per unit side the caller owns or rents (a second flat, or both sides of one)
  const seen = new Set<string>()
  const own = (me?.occupancies ?? []).filter((o) => {
    if (o.organization_id !== orgId || (o.kind !== 'owner' && o.kind !== 'tenant')) return false
    const key = `${o.unit_id}:${o.kind}`
    if (seen.has(key)) return false
    seen.add(key)
    return true
  })
  return (
    <>
      {own.map((o) => (
        <HouseholdSection key={`${o.unit_id}:${o.kind}`} orgId={orgId} unit={o} />
      ))}
    </>
  )
}

type OwnOccupancy = NonNullable<ReturnType<typeof useSession>['me']>['occupancies'][number]

function HouseholdSection({ orgId, unit }: { orgId: string; unit: OwnOccupancy }) {
  const { t, i18n } = useLingui()
  const queryClient = useQueryClient()
  const idem = useIdempotencyKey()
  const applyError = useApplyFormError()

  const invitesQ = useQuery({
    queryKey: ['me.listHouseholdInvitations', orgId, unit.unit_id],
    queryFn: async () => {
      const { data } = await draftApi.GET('/v1/orgs/{org_id}/me/household-invitations', {
        params: { path: { org_id: orgId }, query: { unit_id: unit.unit_id } },
      })
      return data!
    },
  })
  const membersQ = useQuery({
    queryKey: ['people.listOccupancies', orgId, { unit_id: unit.unit_id, kind: 'household' }],
    retry: false,
    queryFn: async () => {
      const { data } = await draftApi.GET('/v1/orgs/{org_id}/occupancies', {
        params: {
          path: { org_id: orgId },
          query: { unit_id: unit.unit_id, kind: 'household', active: true },
        },
      })
      return data!
    },
  })
  const refresh = async () => {
    await queryClient.invalidateQueries({ queryKey: ['me.listHouseholdInvitations', orgId] })
    await queryClient.invalidateQueries({ queryKey: ['people.listOccupancies', orgId] })
  }

  const schema = useMemo(
    () =>
      z
        .object({
          display_name: z
            .string()
            .trim()
            .min(1, t`Enter their name`),
          channel: z.string(),
          destination: z
            .string()
            .trim()
            .min(1, t`Enter their phone number or email address`),
        })
        .refine((v) => v.destination === '' || normalizeDestination(v.channel, v.destination), {
          path: ['destination'],
          message: t`Enter a phone number in international format (+90 555 111 22 33) or a valid email address`,
        }),
    [t],
  )
  const form = useForm({
    resolver: zodResolver(schema),
    defaultValues: { display_name: '', channel: 'sms', destination: '' },
  })
  const invite = useMutation({
    mutationFn: (v: z.infer<typeof schema>) =>
      runWithReauth(() =>
        draftApi.POST('/v1/orgs/{org_id}/units/{unit_id}/household-invitations', {
          params: { path: { org_id: orgId, unit_id: unit.unit_id } },
          body: {
            display_name: v.display_name,
            channel: v.channel === 'email' ? 'email' : 'sms',
            destination: normalizeDestination(v.channel, v.destination) ?? v.destination,
          },
          ...withIdempotencyKey(idem.key),
        }),
      ),
    onSuccess: async () => {
      idem.renew()
      form.reset()
      await refresh()
    },
    onError: (e) => applyError(form, e),
  })
  const revoke = useMutation({
    mutationFn: (id: string) =>
      draftApi.DELETE('/v1/orgs/{org_id}/me/household-invitations/{invitation_id}', {
        params: { path: { org_id: orgId, invitation_id: id } },
      }),
    onSuccess: refresh,
  })
  const end = useMutation({
    mutationFn: (id: string) =>
      draftApi.DELETE('/v1/orgs/{org_id}/me/household-occupancies/{occupancy_id}', {
        params: { path: { org_id: orgId, occupancy_id: id }, query: {} },
      }),
    onSuccess: refresh,
  })
  const { errors } = form.formState

  return (
    <Card role="region" aria-label={t`Household of ${unit.unit_label}`}>
      <CardHeader>
        <CardTitle>
          <Trans>People in my household</Trans>
        </CardTitle>
        <CardDescription>
          <Trans>
            Invite family or flatmates to {unit.unit_label}. They can use requests, announcements
            and bookings but cannot see money unless you allow it.
          </Trans>
        </CardDescription>
      </CardHeader>
      <CardContent className="grid gap-5">
        {invitesQ.error ? (
          <ErrorState error={invitesQ.error} onRetry={() => void invitesQ.refetch()} />
        ) : null}
        {membersQ.data && membersQ.data.items.length > 0 ? (
          <ul aria-label={t`Household members`} className="divide-y rounded-md border text-sm">
            {membersQ.data.items.map((m) => (
              <li key={m.id} className="flex items-center justify-between gap-2 p-3">
                <span className="font-medium">{m.person_display_name}</span>
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={end.isPending}
                  onClick={() => end.mutate(m.id)}
                >
                  <Trans>End membership</Trans>
                </Button>
              </li>
            ))}
          </ul>
        ) : null}
        {invitesQ.data && invitesQ.data.items.length > 0 ? (
          <ul aria-label={t`Invitations I sent`} className="divide-y rounded-md border text-sm">
            {invitesQ.data.items.map((i) => (
              <li key={i.id} className="flex items-center justify-between gap-2 p-3">
                <span dir="ltr">{i.destination}</span>
                <Badge variant={i.status === 'accepted' ? 'success' : 'secondary'}>
                  {inviteStatusLabel(i.status, i18n)}
                </Badge>
                {i.status === 'pending' ? (
                  <Button variant="ghost" size="sm" onClick={() => revoke.mutate(i.id)}>
                    <Trans>Revoke</Trans>
                  </Button>
                ) : null}
              </li>
            ))}
          </ul>
        ) : null}
        <form
          noValidate
          aria-label={t`Invite a household member to ${unit.unit_label}`}
          className="grid gap-3 sm:grid-cols-2"
          onSubmit={form.handleSubmit((v) => invite.mutateAsync(v).catch(() => undefined))}
        >
          <Field
            id={`hh-name-${unit.unit_id}`}
            label={<Trans>Name</Trans>}
            error={errors.display_name?.message}
          >
            <Input
              id={`hh-name-${unit.unit_id}`}
              aria-invalid={!!errors.display_name}
              {...form.register('display_name')}
            />
          </Field>
          <Field id={`hh-channel-${unit.unit_id}`} label={<Trans>Send by</Trans>}>
            <Select id={`hh-channel-${unit.unit_id}`} {...form.register('channel')}>
              <option value="sms">SMS</option>
              <option value="email">{t`Email`}</option>
            </Select>
          </Field>
          <Field
            id={`hh-dest-${unit.unit_id}`}
            label={<Trans>Phone or email</Trans>}
            error={errors.destination?.message}
            className="sm:col-span-2"
          >
            <Input
              id={`hh-dest-${unit.unit_id}`}
              dir="ltr"
              aria-invalid={!!errors.destination}
              {...form.register('destination')}
            />
          </Field>
          <div className="flex flex-col gap-2 sm:col-span-2">
            <FormError message={errors.root?.message} />
            <div>
              <SubmitButton busy={invite.isPending}>
                <Trans>Send invitation</Trans>
              </SubmitButton>
            </div>
          </div>
        </form>
      </CardContent>
    </Card>
  )
}
