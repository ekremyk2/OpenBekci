import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { Trans, useLingui } from '@lingui/react/macro'
import { useSession } from '@/app/session'
import { Money } from '@/components/data/Money'
import { SiteDate } from '@/components/data/SiteDate'
import { SidePicker, type DebtorSide } from '@/components/forms/SidePicker'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableFooter,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { useIdempotencyKey } from '@/lib/idempotency'
import { toast } from '@/lib/toast'
import {
  idemHeader,
  moneyApi,
  startOfYear,
  todayIn,
  unwrap,
  useMoneyCtx,
  useMoneyMutation,
  useMoneyQuery,
  type MoneyCtx,
  type S,
} from './api'
import { useLabels } from './labels'
import { PortalStatement } from './PortalPages'
import { PersonPicker, UnitPicker } from './pickers'
import {
  Checkbox,
  ExportButtons,
  Field,
  Gate,
  PageHeader,
  QueryBoundary,
  Select,
  SideBadge,
} from './ui'

export interface StatementSearch {
  unit?: string
  side?: DebtorSide
  person?: string
}

/** Statement of a unit side or a person: running balance, principal/fee split, accrued fee today. */
export function StatementView({ statement, ctx }: { statement: S['Statement']; ctx: MoneyCtx }) {
  const { t } = useLingui()
  const labels = useLabels()
  return (
    <div className="flex flex-col gap-4">
      <Table aria-label={t`Statement`}>
        <TableHeader>
          <TableRow>
            <TableHead>
              <Trans>Date</Trans>
            </TableHead>
            <TableHead>
              <Trans>Description</Trans>
            </TableHead>
            <TableHead>
              <Trans>Side</Trans>
            </TableHead>
            <TableHead className="text-end">
              <Trans>Principal</Trans>
            </TableHead>
            <TableHead className="text-end">
              <Trans>Late fee</Trans>
            </TableHead>
            <TableHead className="text-end">
              <Trans>Debit</Trans>
            </TableHead>
            <TableHead className="text-end">
              <Trans>Credit</Trans>
            </TableHead>
            <TableHead className="text-end">
              <Trans>Balance</Trans>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow className="bg-muted/30">
            <TableCell colSpan={7}>
              <Trans>Opening balance</Trans>
            </TableCell>
            <TableCell className="text-end">
              <Money minor={statement.opening_balance.amount} currency={ctx.currency} />
            </TableCell>
          </TableRow>
          {statement.lines.map((l, i) => (
            <TableRow key={i}>
              <TableCell>
                <SiteDate value={l.date} timeZone={ctx.timeZone} />
              </TableCell>
              <TableCell>
                {l.description}
                <span className="ms-2 text-xs text-muted-foreground">
                  {labels.stmtKind[l.kind] ?? l.kind}
                </span>
              </TableCell>
              <TableCell>
                <SideBadge side={l.debtor_kind} />
              </TableCell>
              <TableCell className="text-end">
                <Money minor={l.principal.amount} currency={ctx.currency} />
              </TableCell>
              <TableCell className="text-end">
                <Money minor={l.late_fee.amount} currency={ctx.currency} />
              </TableCell>
              <TableCell className="text-end">
                <Money minor={l.debit.amount} currency={ctx.currency} />
              </TableCell>
              <TableCell className="text-end">
                <Money minor={l.credit.amount} currency={ctx.currency} />
              </TableCell>
              <TableCell className="text-end">
                <Money minor={l.balance.amount} currency={ctx.currency} />
              </TableCell>
            </TableRow>
          ))}
          <TableRow>
            <TableCell colSpan={7}>
              <Trans>Closing balance</Trans>
            </TableCell>
            <TableCell className="text-end">
              <Money minor={statement.closing_balance.amount} currency={ctx.currency} />
            </TableCell>
          </TableRow>
        </TableBody>
        <TableFooter>
          <TableRow>
            <TableCell colSpan={7}>
              <Trans>Late fee accrued so far (not yet booked)</Trans>
            </TableCell>
            <TableCell className="text-end" data-testid="accrued-fee">
              <Money minor={statement.accrued_late_fee.amount} currency={ctx.currency} />
            </TableCell>
          </TableRow>
          <TableRow>
            <TableCell colSpan={7}>
              <Trans>What is owed today</Trans>
            </TableCell>
            <TableCell className="text-end" data-testid="total-due">
              <Money minor={statement.total_due.amount} currency={ctx.currency} />
            </TableCell>
          </TableRow>
          {statement.credit.amount > 0 ? (
            <TableRow>
              <TableCell colSpan={7}>
                <Trans>Prepayment credit</Trans>
              </TableCell>
              <TableCell className="text-end">
                <Money minor={statement.credit.amount} currency={ctx.currency} />
              </TableCell>
            </TableRow>
          ) : null}
        </TableFooter>
      </Table>
    </div>
  )
}

export function StatementsPage({ search }: { search: StatementSearch }) {
  const { can } = useSession()
  if (!can('statements.read') && !can('money.read'))
    return <PortalStatement unit={search.unit} side={search.side} />
  return <ManagerStatements search={search} />
}

function ManagerStatements({ search }: { search: StatementSearch }) {
  const ctx = useMoneyCtx()!
  const { t } = useLingui()
  const navigate = useNavigate()
  const today = todayIn(ctx.timeZone)
  const [mode, setMode] = useState<'unit' | 'person'>(search.person ? 'person' : 'unit')
  const [from, setFrom] = useState(startOfYear(today))
  const [to, setTo] = useState(today)
  const unit = search.unit ?? ''
  const side: DebtorSide = search.side ?? 'owner'
  const person = search.person ?? ''
  const setSearch = (s: StatementSearch) => void navigate({ to: '.', search: s, replace: true })
  const ready = mode === 'unit' ? !!unit : !!person

  const q = useMoneyQuery(
    ['statement', mode, unit, side, person, from, to],
    () =>
      mode === 'unit'
        ? unwrap(
            moneyApi.GET('/v1/orgs/{org_id}/units/{unit_id}/statements/{debtor_kind}', {
              params: {
                path: { org_id: ctx.orgId, unit_id: unit, debtor_kind: side },
                query: { from, to },
              },
            }),
          )
        : unwrap(
            moneyApi.GET('/v1/orgs/{org_id}/persons/{person_id}/statement', {
              params: { path: { org_id: ctx.orgId, person_id: person }, query: { from, to } },
            }),
          ),
    { enabled: ready },
  )
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={t`Statements`}
        description={t`Charges, payments and fees with a running balance, for one side of a unit or for one person across units.`}
      />
      <div className="flex flex-wrap items-end gap-4">
        <Field label={t`For`} htmlFor="st-mode">
          <Select
            id="st-mode"
            className="w-auto"
            value={mode}
            onChange={(e) => setMode(e.target.value as 'unit' | 'person')}
          >
            <option value="unit">{t`A unit side`}</option>
            <option value="person">{t`A person`}</option>
          </Select>
        </Field>
        {mode === 'unit' ? (
          <>
            <Field label={t`Unit`} htmlFor="st-unit">
              <UnitPicker
                ctx={ctx}
                id="st-unit"
                value={unit}
                onChange={(id) => setSearch({ unit: id || undefined, side })}
              />
            </Field>
            <Field label={t`Side`}>
              <SidePicker
                value={side}
                onChange={(s) => setSearch({ unit: unit || undefined, side: s })}
              />
            </Field>
          </>
        ) : (
          <Field label={t`Person`} htmlFor="st-person">
            <PersonPicker
              ctx={ctx}
              id="st-person"
              value={person}
              onChange={(id) => setSearch({ person: id || undefined })}
            />
          </Field>
        )}
        <Field label={t`From`} htmlFor="st-from">
          <Input
            id="st-from"
            type="date"
            dir="ltr"
            value={from}
            onChange={(e) => setFrom(e.target.value)}
          />
        </Field>
        <Field label={t`To`} htmlFor="st-to">
          <Input
            id="st-to"
            type="date"
            dir="ltr"
            value={to}
            onChange={(e) => setTo(e.target.value)}
          />
        </Field>
      </div>
      {!ready ? (
        <p className="text-sm text-muted-foreground">
          <Trans>Choose who the statement is for.</Trans>
        </p>
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-3">
            <ExportButtons
              endpoint={
                mode === 'unit'
                  ? '/v1/orgs/{org_id}/units/{unit_id}/statements/{debtor_kind}'
                  : '/v1/orgs/{org_id}/persons/{person_id}/statement'
              }
              pathParams={
                mode === 'unit' ? { unit_id: unit, debtor_kind: side } : { person_id: person }
              }
              query={{ from, to }}
              basename="statement"
              formats={['pdf', 'xlsx', 'csv']}
            />
            <Gate permission="statements.read" fallback={null}>
              <SendStatement
                ctx={ctx}
                mode={mode}
                unit={unit}
                side={side}
                person={person}
                from={from}
                to={to}
              />
            </Gate>
          </div>
          <QueryBoundary
            query={q}
            skeleton="table"
            isEmpty={(s) => s.lines.length === 0 && s.opening_balance.amount === 0}
            emptyTitle={t`Nothing to show for this period`}
          >
            {(s) => <StatementView statement={s} ctx={ctx} />}
          </QueryBoundary>
        </>
      )}
    </div>
  )
}

function SendStatement({
  ctx,
  mode,
  unit,
  side,
  person,
  from,
  to,
}: {
  ctx: MoneyCtx
  mode: 'unit' | 'person'
  unit: string
  side: DebtorSide
  person: string
  from: string
  to: string
}) {
  const { t } = useLingui()
  const idem = useIdempotencyKey()
  const [sms, setSms] = useState(false)
  const [email, setEmail] = useState(true)
  const m = useMoneyMutation(async () => {
    const channels: S['StatementSend']['channels'] = [
      'in_app',
      ...(sms ? (['sms'] as const) : []),
      ...(email ? (['email'] as const) : []),
    ]
    const res = await moneyApi.POST('/v1/orgs/{org_id}/statements:send', {
      params: { path: { org_id: ctx.orgId }, header: idemHeader(idem.key) },
      body:
        mode === 'unit'
          ? { debtor: { unit_id: unit, debtor_kind: side }, from, to, channels }
          : { debtor: { person_id: person }, from, to, channels },
    })
    return res.data!
  })
  return (
    <div className="flex items-center gap-3">
      <Checkbox label={t`Email`} checked={email} onChange={(e) => setEmail(e.target.checked)} />
      <Checkbox label={t`SMS`} checked={sms} onChange={(e) => setSms(e.target.checked)} />
      <Button
        variant="outline"
        size="sm"
        disabled={m.isPending}
        onClick={() =>
          m.mutateAsync().then(
            (r) => {
              idem.renew()
              toast.info(t`Statement sent to ${r.recipient_count} recipient(s).`)
            },
            () => toast.error(t`Could not send the statement.`),
          )
        }
      >
        <Trans>Send to resident</Trans>
      </Button>
    </div>
  )
}
