import { useEffect, useMemo, useState } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Trans, useLingui } from '@lingui/react/macro'
import { AlertTriangleIcon } from 'lucide-react'
import { useSession } from '@/app/session'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { toast } from '@/lib/toast'
import { useGovernanceSettings } from './hooks'
import { gApi, ifMatch, unwrap, useOrgMutation, useOrgQuery, useSiteCtx, type S } from './kit/api'
import {
  applyServerErrors,
  Checkbox,
  Field,
  FormRootError,
  PageHeader,
  QueryBoundary,
  Select,
} from './kit/ui'
import { Callout } from './kit/widgets'
import { THRESHOLD_RULE_LABEL_KEYS } from './hooks'
import { useGovLabels } from './labels'

type Settings = S['GovernanceSettingsSet']

/** zod schema of the settings form: every Law 634 parameter, validated as a number in range. */
export function useSettingsSchema() {
  const { t } = useLingui()
  return useMemo(() => {
    const num = (min: number, max: number, integer: boolean) =>
      z
        .string()
        .trim()
        .refine((s) => s !== '' && Number.isFinite(Number(s.replace(',', '.'))), t`Enter a number.`)
        .refine(
          (s) => Number(s.replace(',', '.')) >= min && Number(s.replace(',', '.')) <= max,
          t`Enter a value from ${min} to ${max}.`,
        )
        .refine(
          (s) => !integer || Number.isInteger(Number(s.replace(',', '.'))),
          t`Enter a whole number.`,
        )
    return z
      .object({
        first_call_by_count_pct: num(0, 100, false),
        first_call_by_share_pct: num(0, 100, false),
        second_call_requires_quorum: z.boolean(),
        thresholds: z.array(
          z.object({
            topic: z.string(),
            rule: z.enum([
              'simple_majority_present',
              'majority_count_and_share',
              'four_fifths',
              'unanimity',
            ]),
            note: z.string().nullable(),
          }),
        ),
        max_votes_per_person_pct: num(0, 100, false),
        proxy_cap_pct: num(0, 100, false),
        proxy_cap_small_building_max: num(0, 1000, true),
        small_building_unit_limit: num(0, 100000, true),
        call_min_days: num(0, 365, true),
        second_call_min_days: num(0, 365, true),
        second_call_max_days: num(0, 365, true),
        term_reminder_days: num(0, 730, true),
      })
      .refine((v) => Number(v.second_call_min_days) <= Number(v.second_call_max_days), {
        path: ['second_call_max_days'],
        message: t`The latest day must not be before the earliest day.`,
      })
  }, [t])
}

type Values = z.infer<ReturnType<typeof useSettingsSchema>>

export const toValues = (s: Settings): Values => ({
  first_call_by_count_pct: String(s.quorum.first_call_by_count_pct),
  first_call_by_share_pct: String(s.quorum.first_call_by_share_pct),
  second_call_requires_quorum: s.quorum.second_call_requires_quorum,
  thresholds: s.thresholds.map((x) => ({
    topic: x.topic,
    rule: (THRESHOLD_RULE_LABEL_KEYS as readonly string[]).includes(x.rule)
      ? (x.rule as Values['thresholds'][number]['rule'])
      : 'simple_majority_present',
    note: x.note,
  })),
  max_votes_per_person_pct: String(s.max_votes_per_person_pct),
  proxy_cap_pct: String(s.proxy_cap_pct),
  proxy_cap_small_building_max: String(s.proxy_cap_small_building_max),
  small_building_unit_limit: String(s.small_building_unit_limit),
  call_min_days: String(s.call_min_days),
  second_call_min_days: String(s.second_call_min_days),
  second_call_max_days: String(s.second_call_max_days),
  term_reminder_days: String(s.term_reminder_days),
})

const n = (s: string) => Number(s.replace(',', '.'))
export const toSettings = (v: Values): Settings => ({
  quorum: {
    first_call_by_count_pct: n(v.first_call_by_count_pct),
    first_call_by_share_pct: n(v.first_call_by_share_pct),
    second_call_requires_quorum: v.second_call_requires_quorum,
  },
  thresholds: v.thresholds,
  max_votes_per_person_pct: n(v.max_votes_per_person_pct),
  proxy_cap_pct: n(v.proxy_cap_pct),
  proxy_cap_small_building_max: n(v.proxy_cap_small_building_max),
  small_building_unit_limit: n(v.small_building_unit_limit),
  call_min_days: n(v.call_min_days),
  second_call_min_days: n(v.second_call_min_days),
  second_call_max_days: n(v.second_call_max_days),
  term_reminder_days: n(v.term_reminder_days),
})

/** Site settings of the assembly: every legal parameter, with Law 634 defaults and their sources. */
export function SettingsPage() {
  const { t } = useLingui()
  const labels = useGovLabels()
  const { can } = useSession()
  const ctx = useSiteCtx()!
  const manage = can('meetings.manage')
  const settings = useGovernanceSettings(ctx.orgId, ctx.siteId)
  const presets = useOrgQuery(['presets'], () =>
    unwrap(
      gApi.GET('/v1/orgs/{org_id}/governance-presets', { params: { path: { org_id: ctx.orgId } } }),
    ).then((r) => r.items),
  )
  const schema = useSettingsSchema()
  const form = useForm<Values>({ resolver: zodResolver(schema) })
  const [presetId, setPresetId] = useState('')
  const loaded = settings.data
  useEffect(() => {
    if (loaded) form.reset(toValues(loaded.settings))
    // reset only when a new version arrives
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loaded?.version])
  const save = useOrgMutation(async (v: Values) =>
    unwrap(
      gApi.PUT('/v1/orgs/{org_id}/sites/{site_id}/governance-settings', {
        params: {
          path: { org_id: ctx.orgId, site_id: ctx.siteId },
          header: ifMatch(loaded!.version),
        },
        body: toSettings(v),
      }),
    ),
  )
  const err = form.formState.errors
  const num = (name: keyof Values, label: string, hint?: string, suffix?: string) => (
    <Field
      label={label}
      htmlFor={`gs-${name}`}
      error={(err[name] as { message?: string } | undefined)?.message}
      hint={hint}
    >
      <div className="flex items-center gap-2">
        <Input
          id={`gs-${name}`}
          inputMode="decimal"
          dir="ltr"
          disabled={!manage}
          {...form.register(name as 'call_min_days')}
        />
        {suffix ? <span className="text-sm text-muted-foreground">{suffix}</span> : null}
      </div>
    </Field>
  )
  const preset = presets.data?.find((p) => p.id === presetId)
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={t`Assembly settings`}
        description={t`Quorum, thresholds, proxy limits and timing. Law 634 values are the defaults; change them only where your yönetim planı allows it.`}
      />
      <QueryBoundary query={settings} skeleton="page">
        {(s) => (
          <form
            className="flex flex-col gap-4"
            noValidate
            onSubmit={form.handleSubmit(async (v) => {
              try {
                await save.mutateAsync(v)
                toast.info(t`Settings saved.`)
              } catch (e) {
                applyServerErrors(form, e, t`Could not save the settings.`)
              }
            })}
          >
            {s.preset_notes.length > 0 ? (
              <Card>
                <CardHeader>
                  <CardTitle>
                    <Trans>Where the defaults come from</Trans>
                    {s.preset ? (
                      <Badge variant="outline" className="ms-2">
                        {s.preset}
                      </Badge>
                    ) : null}
                  </CardTitle>
                </CardHeader>
                <CardContent>
                  <ul className="flex flex-col gap-2 text-sm">
                    {s.preset_notes.map((note) => (
                      <li key={note.key} className="flex items-start gap-2">
                        {note.verify ? (
                          <Badge variant="warning" className="shrink-0 gap-1">
                            <AlertTriangleIcon className="size-3" aria-hidden="true" />
                            <Trans>To be verified</Trans>
                          </Badge>
                        ) : null}
                        <span>{note.note}</span>
                      </li>
                    ))}
                  </ul>
                </CardContent>
              </Card>
            ) : null}
            {manage && (presets.data ?? []).length > 0 ? (
              <Card>
                <CardContent className="flex flex-wrap items-end gap-3 pt-6">
                  <Field label={t`Load a preset`} htmlFor="gs-preset" className="min-w-60">
                    <Select
                      id="gs-preset"
                      value={presetId}
                      onChange={(e) => setPresetId(e.target.value)}
                    >
                      <option value="">{t`Choose a preset`}</option>
                      {(presets.data ?? []).map((p) => (
                        <option key={p.id} value={p.id}>
                          {p.name}
                        </option>
                      ))}
                    </Select>
                  </Field>
                  <Button
                    type="button"
                    variant="outline"
                    disabled={!preset}
                    onClick={() => preset && form.reset(toValues(preset.settings))}
                  >
                    <Trans>Fill the form with this preset</Trans>
                  </Button>
                </CardContent>
              </Card>
            ) : null}
            <Card>
              <CardHeader>
                <CardTitle>
                  <Trans>Quorum</Trans>
                </CardTitle>
              </CardHeader>
              <CardContent className="grid gap-4 sm:grid-cols-2">
                {num(
                  'first_call_by_count_pct',
                  t`First call: more than this share of owners`,
                  undefined,
                  '%',
                )}
                {num(
                  'first_call_by_share_pct',
                  t`First call: more than this share of the land`,
                  undefined,
                  '%',
                )}
                <div className="sm:col-span-2">
                  <Checkbox
                    label={t`The second call also needs a quorum`}
                    disabled={!manage}
                    {...form.register('second_call_requires_quorum')}
                  />
                  <p className="mt-1 text-xs text-muted-foreground">
                    <Trans>
                      Default: no quorum on the second call; decisions by majority of those present.
                    </Trans>
                  </p>
                </div>
              </CardContent>
            </Card>
            <Card>
              <CardHeader>
                <CardTitle>
                  <Trans>Decision thresholds by topic</Trans>
                </CardTitle>
              </CardHeader>
              <CardContent>
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>
                        <Trans>Topic</Trans>
                      </TableHead>
                      <TableHead>
                        <Trans>Rule</Trans>
                      </TableHead>
                      <TableHead>
                        <Trans>Source</Trans>
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {s.settings.thresholds.map((th, i) => (
                      <TableRow key={th.topic}>
                        <TableCell>{labels.topic(th.topic)}</TableCell>
                        <TableCell>
                          <Select
                            aria-label={t`Rule for ${labels.topic(th.topic)}`}
                            disabled={!manage}
                            {...form.register(`thresholds.${i}.rule`)}
                          >
                            {THRESHOLD_RULE_LABEL_KEYS.map((r) => (
                              <option key={r} value={r}>
                                {labels.rule(r)}
                              </option>
                            ))}
                          </Select>
                        </TableCell>
                        <TableCell className="text-xs text-muted-foreground">
                          {th.note ?? '–'}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </CardContent>
            </Card>
            <Card>
              <CardHeader>
                <CardTitle>
                  <Trans>Votes and proxies</Trans>
                </CardTitle>
              </CardHeader>
              <CardContent className="grid gap-4 sm:grid-cols-2">
                {num(
                  'max_votes_per_person_pct',
                  t`Most votes one person may hold`,
                  t`Law 634 default: one third.`,
                  '%',
                )}
                {num(
                  'proxy_cap_pct',
                  t`Most votes one person may represent by proxy`,
                  undefined,
                  '%',
                )}
                {num('small_building_unit_limit', t`Small building: up to this many units`)}
                {num('proxy_cap_small_building_max', t`Small building: most proxies per person`)}
              </CardContent>
            </Card>
            <Card>
              <CardHeader>
                <CardTitle>
                  <Trans>Timing</Trans>
                </CardTitle>
              </CardHeader>
              <CardContent className="grid gap-4 sm:grid-cols-2">
                {num(
                  'call_min_days',
                  t`Send the call at least this many days before`,
                  undefined,
                  t`days`,
                )}
                {num('term_reminder_days', t`Remind before a term ends`, undefined, t`days`)}
                {num(
                  'second_call_min_days',
                  t`Second meeting: earliest, days after the first`,
                  undefined,
                  t`days`,
                )}
                {num(
                  'second_call_max_days',
                  t`Second meeting: latest, days after the first`,
                  undefined,
                  t`days`,
                )}
              </CardContent>
            </Card>
            <Callout tone="info">
              <Trans>
                Changes apply from the next assembly; an assembly that was already called keeps the
                rules it was called under.
              </Trans>
            </Callout>
            <FormRootError message={form.formState.errors.root?.message} />
            {manage ? (
              <div>
                <Button type="submit" disabled={save.isPending}>
                  <Trans>Save settings</Trans>
                </Button>
              </div>
            ) : null}
          </form>
        )}
      </QueryBoundary>
    </div>
  )
}
