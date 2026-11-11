import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useLingui } from '@lingui/react/macro'
import { useSession } from '@/app/session'
import { Input } from '@/components/ui/input'
import { moneyApi, REFERENCE_STALE, unwrap, useMoneyQuery, type MoneyCtx, type S } from './api'
import { Select } from './ui'
import { EntityPicker, type PickerOption } from '@/components/forms/EntityPicker'

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

/**
 * EVERY unit of the site, following the cursor (the contract caps a page at 200). For screens that
 * must show or fill each unit, such as opening balances; pickers use server-side search instead.
 */
export function useAllUnits(ctx: MoneyCtx) {
  return useMoneyQuery(
    ['units-all', ctx.siteId],
    async () => {
      const all: S['Unit'][] = []
      let cursor: string | undefined
      for (let page = 0; page < 50; page++) {
        const r = await unwrap(
          moneyApi.GET('/v1/orgs/{org_id}/units', {
            params: {
              path: { org_id: ctx.orgId },
              query: { site_id: ctx.siteId, limit: 200, cursor },
            },
          }),
        )
        all.push(...r.items)
        if (!r.next_cursor) break
        cursor = r.next_cursor
      }
      return all
    },
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

/**
 * The signed-in user's person in this site. `/v1/me` carries no person id, so the person is found
 * by `user_id` among the persons matching the user's name (server-side `q`).
 * CONTRACT GAP: a cash collector without `person.read` cannot list persons; `/v1/me` should carry
 * `person_id` (reported on the PR).
 */
export function useMyPersonId(ctx: MoneyCtx) {
  const { me } = useSession()
  const userId = me?.user.id
  const name = me?.user.display_name ?? ''
  return useMoneyQuery(
    ['my-person', ctx.siteId, userId],
    async () => {
      const fromMe = (me?.user as { person_id?: string } | undefined)?.person_id
      if (fromMe) return fromMe
      const r = await unwrap(
        moneyApi.GET('/v1/orgs/{org_id}/persons', {
          params: {
            path: { org_id: ctx.orgId },
            query: { site_id: ctx.siteId, q: name || undefined, limit: 50 },
          },
        }),
      )
      return r.items.find((p) => p.user_id === userId)?.id ?? null
    },
    { staleTime: REFERENCE_STALE, enabled: !!userId },
  )
}

/** The cash box whose collector is the signed-in person (M5): where their cash receipts go. */
export function useMyCashBox(ctx: MoneyCtx) {
  const person = useMyPersonId(ctx)
  const boxes = useCashBoxes(ctx)
  const box = person.data
    ? boxes.data?.find((b) => b.collector_person_id === person.data)
    : undefined
  return {
    box: box ?? null,
    isPending: person.isPending || boxes.isPending,
  }
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

type PickerProps = {
  id?: string
  value: string
  onChange: (id: string) => void
  placeholder?: string
  disabled?: boolean
  className?: string
  'aria-label'?: string
  'aria-invalid'?: boolean | 'true' | 'false'
}

/**
 * Money forms keep entity ids as plain strings; the shared `EntityPicker` works with an option
 * (id + label). This bridges the two: the label of a preset id is looked up through `lookup`.
 */
function useOption(
  value: string,
  key: readonly unknown[],
  lookup: (id: string) => Promise<PickerOption | undefined>,
) {
  const [chosen, setChosen] = useState<PickerOption | null>(null)
  const preset = useQuery({
    queryKey: [...key, 'label', value],
    queryFn: async () => (await lookup(value)) ?? { id: value, label: value },
    enabled: !!value && chosen?.id !== value,
    staleTime: REFERENCE_STALE,
  })
  const option: PickerOption | null = !value
    ? null
    : chosen?.id === value
      ? chosen
      : (preset.data ?? { id: value, label: '…' })
  return { option, setChosen }
}

type SearchPickerProps = PickerProps & {
  queryKey: readonly unknown[]
  search: (q: string) => Promise<PickerOption[]>
  pageSize?: number
}

/** Searchable picker over a server list or a small fully loaded one (the search then filters locally). */
function SearchPicker({
  id = 'picker',
  value,
  onChange,
  queryKey,
  search,
  className,
  disabled,
  placeholder,
  pageSize = 200,
  ...aria
}: SearchPickerProps) {
  const { option, setChosen } = useOption(value, queryKey, async (v) =>
    (await search('')).find((o) => o.id === v),
  )
  return (
    <div className={className}>
      <EntityPicker
        id={id}
        queryKey={queryKey}
        pageSize={pageSize}
        value={option}
        search={search}
        disabled={disabled}
        placeholder={placeholder}
        invalid={aria['aria-invalid'] === true || aria['aria-invalid'] === 'true'}
        aria-label={aria['aria-label']}
        onChange={(o) => {
          setChosen(o)
          onChange(o?.id ?? '')
        }}
      />
    </div>
  )
}

const matches = (label: string, q: string) =>
  label.toLocaleLowerCase().includes(q.toLocaleLowerCase())

export function UnitPicker({ ctx, ...p }: PickerProps & { ctx: MoneyCtx }) {
  return (
    <SearchPicker
      {...p}
      queryKey={['unit-picker', ctx.orgId, ctx.siteId]}
      search={async (q) => {
        const r = await unwrap(
          moneyApi.GET('/v1/orgs/{org_id}/units', {
            params: {
              path: { org_id: ctx.orgId },
              query: { site_id: ctx.siteId, q: q || undefined, limit: 200 },
            },
          }),
        )
        return r.items.map((u) => ({ id: u.id, label: u.door_number, hint: u.type }))
      }}
    />
  )
}

export function PersonPicker({ ctx, ...p }: PickerProps & { ctx: MoneyCtx }) {
  return (
    <SearchPicker
      {...p}
      queryKey={['person-picker', ctx.orgId, ctx.siteId]}
      search={async (q) => {
        const r = await unwrap(
          moneyApi.GET('/v1/orgs/{org_id}/persons', {
            params: {
              path: { org_id: ctx.orgId },
              query: { site_id: ctx.siteId, q: q || undefined, limit: 200 },
            },
          }),
        )
        return r.items.map((u) => ({
          id: u.id,
          label: u.display_name,
          hint: u.contact.phone ?? u.contact.email ?? undefined,
        }))
      }}
    />
  )
}

export function VendorPicker({ ctx, ...p }: PickerProps & { ctx: MoneyCtx }) {
  return (
    <SearchPicker
      {...p}
      queryKey={['vendor-picker', ctx.orgId]}
      search={async (q) => {
        const r = await unwrap(
          moneyApi.GET('/v1/orgs/{org_id}/vendors', {
            params: { path: { org_id: ctx.orgId }, query: { q: q || undefined, limit: 200 } },
          }),
        )
        return r.items.map((u) => ({ id: u.id, label: u.name }))
      }}
    />
  )
}

/** Accounts of a subtype (or all), optionally narrowed to the ids in `only`. */
export function AccountPicker({
  ctx,
  subtype,
  only,
  ...p
}: PickerProps & { ctx: MoneyCtx; subtype?: string; only?: string[] }) {
  return (
    <SearchPicker
      {...p}
      queryKey={['account-picker', ctx.siteId, subtype ?? 'all', only ?? null]}
      search={async (q) => {
        const r = await unwrap(
          moneyApi.GET('/v1/orgs/{org_id}/accounts', {
            params: {
              path: { org_id: ctx.orgId },
              query: {
                site_id: ctx.siteId,
                subtype,
                q: q || undefined,
                archived: false,
                limit: 200,
              },
            },
          }),
        )
        const items = only ? r.items.filter((a) => only.includes(a.id)) : r.items
        return items.map((a) => ({ id: a.id, label: `${a.code} ${a.name}` }))
      }}
    />
  )
}

/** Small, fully loaded lists (blocks, tags, keys, funds): the search filters them locally. */
function ListPicker({
  items,
  queryKey,
  ...p
}: PickerProps & { items: PickerOption[]; queryKey: readonly unknown[] }) {
  return (
    <SearchPicker
      {...p}
      queryKey={[...queryKey, items.map((i) => i.id).join(',')]}
      search={async (q) => items.filter((i) => !q || matches(i.label, q))}
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
  return <ListPicker {...p} queryKey={['cash-or-bank', ctx.siteId]} items={items} />
}

export function FundPicker({ ctx, ...p }: PickerProps & { ctx: MoneyCtx }) {
  const q = useFunds(ctx)
  return (
    <ListPicker
      {...p}
      queryKey={['fund-picker', ctx.siteId]}
      items={(q.data ?? []).map((f) => ({ id: f.id, label: f.name }))}
    />
  )
}

export function AllocationKeyPicker({ ctx, ...p }: PickerProps & { ctx: MoneyCtx }) {
  const q = useAllocationKeys(ctx)
  return (
    <ListPicker
      {...p}
      queryKey={['key-picker', ctx.siteId]}
      items={(q.data ?? []).map((k) => ({ id: k.id, label: k.name }))}
    />
  )
}

/** Scope of a cost split: the whole site, or one block (unit tags are not served). */
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
      </Select>
      {value.type === 'block' ? (
        <ListPicker
          aria-label={t`Block`}
          queryKey={['block-picker', ctx.siteId]}
          value={value.id ?? ''}
          onChange={(id) => onChange({ type: 'block', id: id || null })}
          items={(blocks.data ?? []).map((b) => ({ id: b.id, label: b.name }))}
        />
      ) : null}
    </div>
  )
}

/** Free-text filter input used by list screens. */
export function SearchBox(props: React.ComponentProps<typeof Input>) {
  return <Input type="search" {...props} />
}
