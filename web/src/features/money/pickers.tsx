import { useLingui } from '@lingui/react/macro'
import { Input } from '@/components/ui/input'
import { moneyApi, REFERENCE_STALE, unwrap, useMoneyQuery, type MoneyCtx, type S } from './api'
import { Select } from './ui'

/* Reference data for pickers. Five minutes of staleness is fine (web plan §6); one page of 200. */

export function useUnits(ctx: MoneyCtx) {
  return useMoneyQuery(
    ['units', ctx.siteId],
    () =>
      unwrap(
        moneyApi.GET('/v1/orgs/{org_id}/units', {
          params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, limit: 200 } },
        }),
      ).then((r) => r.items),
    { staleTime: REFERENCE_STALE },
  )
}

export function usePersons(ctx: MoneyCtx) {
  return useMoneyQuery(
    ['persons', ctx.siteId],
    () =>
      unwrap(
        moneyApi.GET('/v1/orgs/{org_id}/persons', {
          params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, limit: 200 } },
        }),
      ).then((r) => r.items),
    { staleTime: REFERENCE_STALE },
  )
}

export function useVendors(ctx: MoneyCtx) {
  return useMoneyQuery(
    ['vendors'],
    () =>
      unwrap(
        moneyApi.GET('/v1/orgs/{org_id}/vendors', {
          params: { path: { org_id: ctx.orgId }, query: { limit: 200 } },
        }),
      ).then((r) => r.items),
    { staleTime: REFERENCE_STALE },
  )
}

export function useAccounts(ctx: MoneyCtx, subtype?: string) {
  return useMoneyQuery(
    ['accounts', ctx.siteId, subtype ?? 'all'],
    () =>
      unwrap(
        moneyApi.GET('/v1/orgs/{org_id}/accounts', {
          params: {
            path: { org_id: ctx.orgId },
            query: { site_id: ctx.siteId, subtype, archived: false, limit: 200 },
          },
        }),
      ).then((r) => r.items),
    { staleTime: REFERENCE_STALE },
  )
}

export function useFunds(ctx: MoneyCtx) {
  return useMoneyQuery(
    ['funds', ctx.siteId],
    () =>
      unwrap(
        moneyApi.GET('/v1/orgs/{org_id}/funds', {
          params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, limit: 100 } },
        }),
      ).then((r) => r.items),
    { staleTime: REFERENCE_STALE },
  )
}

export function useCashBoxes(ctx: MoneyCtx) {
  return useMoneyQuery(['cash-boxes', ctx.siteId], () =>
    unwrap(
      moneyApi.GET('/v1/orgs/{org_id}/cash-boxes', {
        params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, limit: 100 } },
      }),
    ).then((r) => r.items),
  )
}

export function useAllocationKeys(ctx: MoneyCtx) {
  return useMoneyQuery(
    ['allocation-keys', ctx.siteId],
    () =>
      unwrap(
        moneyApi.GET('/v1/orgs/{org_id}/allocation-keys', {
          params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, limit: 100 } },
        }),
      ).then((r) => r.items),
    { staleTime: REFERENCE_STALE },
  )
}

export function useBlocks(ctx: MoneyCtx) {
  return useMoneyQuery(
    ['blocks', ctx.siteId],
    () =>
      unwrap(
        moneyApi.GET('/v1/orgs/{org_id}/blocks', {
          params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, limit: 100 } },
        }),
      ).then((r) => r.items),
    { staleTime: REFERENCE_STALE },
  )
}

export function useUnitTags(ctx: MoneyCtx) {
  return useMoneyQuery(
    ['unit-tags', ctx.siteId],
    () =>
      unwrap(
        moneyApi.GET('/v1/orgs/{org_id}/unit-tags', {
          params: { path: { org_id: ctx.orgId }, query: { site_id: ctx.siteId, limit: 100 } },
        }),
      ).then((r) => r.items),
    { staleTime: REFERENCE_STALE },
  )
}

type PickerProps = Omit<React.ComponentProps<typeof Select>, 'onChange' | 'value'> & {
  value: string
  onChange: (id: string) => void
  placeholder?: string
}

function Picker({
  items,
  value,
  onChange,
  placeholder,
  ...rest
}: PickerProps & { items: { id: string; label: string }[] }) {
  const { t } = useLingui()
  return (
    <Select {...rest} value={value} onChange={(e) => onChange(e.target.value)}>
      <option value="">{placeholder ?? t`Select…`}</option>
      {items.map((i) => (
        <option key={i.id} value={i.id}>
          {i.label}
        </option>
      ))}
    </Select>
  )
}

export function UnitPicker({ ctx, ...p }: PickerProps & { ctx: MoneyCtx }) {
  const q = useUnits(ctx)
  return (
    <Picker
      {...p}
      disabled={p.disabled || q.isPending}
      items={(q.data ?? []).map((u) => ({ id: u.id, label: u.door_number }))}
    />
  )
}

export function PersonPicker({ ctx, ...p }: PickerProps & { ctx: MoneyCtx }) {
  const q = usePersons(ctx)
  return (
    <Picker
      {...p}
      disabled={p.disabled || q.isPending}
      items={(q.data ?? []).map((u) => ({ id: u.id, label: u.display_name }))}
    />
  )
}

export function VendorPicker({ ctx, ...p }: PickerProps & { ctx: MoneyCtx }) {
  const q = useVendors(ctx)
  return (
    <Picker
      {...p}
      disabled={p.disabled || q.isPending}
      items={(q.data ?? []).map((u) => ({ id: u.id, label: u.name }))}
    />
  )
}

export function AccountPicker({
  ctx,
  subtype,
  ...p
}: PickerProps & { ctx: MoneyCtx; subtype?: string }) {
  const q = useAccounts(ctx, subtype)
  return (
    <Picker
      {...p}
      disabled={p.disabled || q.isPending}
      items={(q.data ?? []).map((a) => ({ id: a.id, label: `${a.code} ${a.name}` }))}
    />
  )
}

/** Cash boxes and bank accounts: everything money can be paid into or out of. */
export function CashOrBankPicker({ ctx, ...p }: PickerProps & { ctx: MoneyCtx }) {
  const cash = useAccounts(ctx, 'cash_box')
  const bank = useAccounts(ctx, 'bank')
  const items = [...(cash.data ?? []), ...(bank.data ?? [])].map((a) => ({
    id: a.id,
    label: `${a.code} ${a.name}`,
  }))
  return <Picker {...p} disabled={p.disabled || cash.isPending || bank.isPending} items={items} />
}

export function FundPicker({ ctx, ...p }: PickerProps & { ctx: MoneyCtx }) {
  const q = useFunds(ctx)
  return (
    <Picker
      {...p}
      disabled={p.disabled || q.isPending}
      items={(q.data ?? []).map((f) => ({ id: f.id, label: f.name }))}
    />
  )
}

export function AllocationKeyPicker({ ctx, ...p }: PickerProps & { ctx: MoneyCtx }) {
  const q = useAllocationKeys(ctx)
  return (
    <Picker
      {...p}
      disabled={p.disabled || q.isPending}
      items={(q.data ?? []).map((k) => ({ id: k.id, label: k.name }))}
    />
  )
}

/** Scope of a cost split: the whole site, one block, or the units carrying a tag. */
export function ScopePicker({
  ctx,
  value,
  onChange,
  idPrefix = 'scope',
}: {
  ctx: MoneyCtx
  value: S['ChargeScope']
  onChange: (scope: S['ChargeScope']) => void
  idPrefix?: string
}) {
  const { t } = useLingui()
  const blocks = useBlocks(ctx)
  const tags = useUnitTags(ctx)
  return (
    <div className="grid gap-2 sm:grid-cols-2">
      <Select
        id={`${idPrefix}-type`}
        aria-label={t`Scope`}
        value={value.type}
        onChange={(e) => onChange({ type: e.target.value as S['ChargeScope']['type'], id: null })}
      >
        <option value="site">{t`Whole site`}</option>
        <option value="block">{t`One block`}</option>
        <option value="unit_tag">{t`Units with a tag`}</option>
      </Select>
      {value.type === 'block' ? (
        <Picker
          aria-label={t`Block`}
          value={value.id ?? ''}
          onChange={(id) => onChange({ type: 'block', id: id || null })}
          items={(blocks.data ?? []).map((b) => ({ id: b.id, label: b.name }))}
        />
      ) : null}
      {value.type === 'unit_tag' ? (
        <Picker
          aria-label={t`Tag`}
          value={value.id ?? ''}
          onChange={(id) => onChange({ type: 'unit_tag', id: id || null })}
          items={(tags.data ?? []).map((g) => ({ id: g.id, label: g.name }))}
        />
      ) : null}
    </div>
  )
}

/** Free-text filter input used by list screens. */
export function SearchBox(props: React.ComponentProps<typeof Input>) {
  return <Input type="search" {...props} />
}
