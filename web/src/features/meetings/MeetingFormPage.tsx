import { useMemo, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Trans, useLingui } from '@lingui/react/macro'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { useIdempotencyKey } from '@/lib/idempotency'
import { addDays, localInputToInstant, todayIn } from './kit/time'
import { gApi, idemHeader, unwrap, useOrgMutation, useSiteCtx } from './kit/api'
import { applyServerErrors, Field, FormRootError, PageHeader, Select } from './kit/ui'
import { Callout } from './kit/widgets'
import { AgendaBuilder, type DraftItem } from './AgendaBuilder'
import { useGovernanceSettings } from './hooks'
import { callTimingIssues } from './logic'
import { useGovLabels } from './labels'

export function MeetingFormPage() {
  const { t } = useLingui()
  const labels = useGovLabels()
  const ctx = useSiteCtx()!
  const navigate = useNavigate()
  const idem = useIdempotencyKey()
  const settings = useGovernanceSettings(ctx.orgId, ctx.siteId)
  const [items, setItems] = useState<DraftItem[]>([])

  const schema = useMemo(
    () =>
      z
        .object({
          kind: z.enum(['general_assembly', 'extraordinary']),
          title: z.string().max(200),
          scheduled: z.string().min(1, t`Choose a date and time.`),
          second: z.string().min(1, t`Choose a date and time.`),
          location: z.string().trim().min(1, t`Enter the place of the assembly.`),
        })
        .refine((v) => !v.scheduled || !v.second || v.second > v.scheduled, {
          path: ['second'],
          message: t`The second meeting must come after the first.`,
        }),
    [t],
  )
  type Values = z.infer<typeof schema>
  const defaultDay = addDays(todayIn(ctx.timeZone), 21)
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: {
      kind: 'general_assembly',
      title: '',
      scheduled: `${defaultDay}T19:00`,
      second: `${addDays(defaultDay, 10)}T19:00`,
      location: '',
    },
  })
  const watched = form.watch()
  const issues =
    settings.data && watched.scheduled && watched.second
      ? callTimingIssues(
          settings.data.settings,
          localInputToInstant(watched.scheduled, ctx.timeZone),
          localInputToInstant(watched.second, ctx.timeZone),
        )
      : []

  const create = useOrgMutation(async (v: Values) => {
    const meeting = await unwrap(
      gApi.POST('/v1/orgs/{org_id}/meetings', {
        params: { path: { org_id: ctx.orgId }, header: idemHeader(idem.key) },
        body: {
          site_id: ctx.siteId,
          kind: v.kind,
          title: v.title.trim() === '' ? null : v.title.trim(),
          scheduled_at: localInputToInstant(v.scheduled, ctx.timeZone),
          second_call_at: localInputToInstant(v.second, ctx.timeZone),
          location: v.location.trim(),
        },
      }),
    )
    for (const [i, it] of items.entries()) {
      await gApi.POST('/v1/orgs/{org_id}/meetings/{meeting_id}/agenda-items', {
        params: {
          path: { org_id: ctx.orgId, meeting_id: meeting.id },
          header: idemHeader(`${idem.key}-agenda-${i}`),
        },
        body: {
          title: it.title,
          description: it.description.trim() === '' ? null : it.description.trim(),
          will_be_voted: it.will_be_voted,
          topic: it.topic === '' ? null : it.topic,
        },
      })
    }
    return meeting
  })

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={t`New assembly`}
        description={t`Set the date, build the agenda, then send the call from the assembly page.`}
      />
      <form
        className="flex flex-col gap-4"
        noValidate
        onSubmit={form.handleSubmit(async (v) => {
          try {
            const m = await create.mutateAsync(v)
            await navigate({
              to: '/orgs/$orgId/meetings/$meetingId',
              params: { orgId: ctx.orgId, meetingId: m.id },
            })
          } catch (e) {
            applyServerErrors(form, e, t`Could not create the assembly.`)
          }
        })}
      >
        <Card>
          <CardHeader>
            <CardTitle>
              <Trans>Date and place</Trans>
            </CardTitle>
          </CardHeader>
          <CardContent className="grid gap-4 sm:grid-cols-2">
            <Field label={t`Kind`} htmlFor="m-kind">
              <Select id="m-kind" {...form.register('kind')}>
                <option value="general_assembly">{labels.meetingKind('general_assembly')}</option>
                <option value="extraordinary">{labels.meetingKind('extraordinary')}</option>
              </Select>
            </Field>
            <Field label={t`Title (optional)`} htmlFor="m-title" error={form.formState.errors.title?.message}>
              <Input id="m-title" {...form.register('title')} />
            </Field>
            <Field
              label={t`First meeting`}
              htmlFor="m-sched"
              error={form.formState.errors.scheduled?.message}
              hint={t`Times are in the site's time zone (${ctx.timeZone}).`}
            >
              <Input id="m-sched" type="datetime-local" dir="ltr" {...form.register('scheduled')} />
            </Field>
            <Field
              label={t`Second meeting`}
              htmlFor="m-second"
              error={form.formState.errors.second?.message}
            >
              <Input id="m-second" type="datetime-local" dir="ltr" {...form.register('second')} />
            </Field>
            <Field
              label={t`Place`}
              htmlFor="m-loc"
              error={form.formState.errors.location?.message}
              className="sm:col-span-2"
            >
              <Input id="m-loc" {...form.register('location')} />
            </Field>
            {settings.data && issues.length > 0 ? (
              <Callout tone="warning" role="status" className="sm:col-span-2">
                <ul className="list-disc ps-5">
                  {issues.includes('call_too_late') ? (
                    <li>
                      <Trans>
                        The call must go out at least {settings.data.settings.call_min_days} days before the
                        meeting. Move the date or send the call later only if the rules allow it.
                      </Trans>
                    </li>
                  ) : null}
                  {issues.includes('second_call_timing') ? (
                    <li>
                      <Trans>
                        The second meeting should be {settings.data.settings.second_call_min_days} to{' '}
                        {settings.data.settings.second_call_max_days} days after the first.
                      </Trans>
                    </li>
                  ) : null}
                </ul>
              </Callout>
            ) : null}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>
              <Trans>Agenda</Trans>
            </CardTitle>
          </CardHeader>
          <CardContent>
            <AgendaBuilder
              items={items}
              onChange={setItems}
              thresholds={settings.data?.settings.thresholds}
            />
          </CardContent>
        </Card>
        <FormRootError message={form.formState.errors.root?.message} />
        <div>
          <Button type="submit" disabled={create.isPending}>
            <Trans>Create assembly</Trans>
          </Button>
        </div>
      </form>
    </div>
  )
}
