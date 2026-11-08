import { useLingui } from '@lingui/react/macro'
import { SidePicker } from '@/components/forms/SidePicker'
import { personRef, unitSideRef, type MoneyCtx, type S, type Side } from './api'
import { PersonPicker, UnitPicker } from './pickers'
import { Field, Select } from './ui'

/**
 * Who a posting is for: one side of a unit, or a person (carried debt after a move-out follows the
 * person, not the unit). Maps onto the contract's `DebtorRef`, exactly one of the two modes.
 */
export type DebtorTarget =
  { mode: 'unit'; unitId: string; side: Side } | { mode: 'person'; personId: string }

export const emptyTarget = (): DebtorTarget => ({ mode: 'unit', unitId: '', side: 'owner' })

export const targetId = (t: DebtorTarget) => (t.mode === 'unit' ? t.unitId : t.personId)

/** `DebtorRef` request value for a target: the contract tells the two modes apart by `mode`. */
export function debtorRef(t: DebtorTarget): S['DebtorRef'] {
  return t.mode === 'unit' ? unitSideRef(t.unitId, t.side) : personRef(t.personId)
}

export function DebtorPicker({
  ctx,
  value,
  onChange,
  idPrefix = 'debtor',
  error,
  allowPerson = true,
}: {
  ctx: MoneyCtx
  value: DebtorTarget
  onChange: (t: DebtorTarget) => void
  idPrefix?: string
  error?: string
  allowPerson?: boolean
}) {
  const { t } = useLingui()
  return (
    <div className="flex flex-col gap-3">
      {allowPerson ? (
        <Field label={t`For`} htmlFor={`${idPrefix}-mode`}>
          <Select
            id={`${idPrefix}-mode`}
            className="w-auto"
            value={value.mode}
            onChange={(e) =>
              onChange(
                e.target.value === 'person'
                  ? { mode: 'person', personId: '' }
                  : { mode: 'unit', unitId: '', side: 'owner' },
              )
            }
          >
            <option value="unit">{t`A unit side`}</option>
            <option value="person">{t`A person (carried debt)`}</option>
          </Select>
        </Field>
      ) : null}
      {value.mode === 'unit' ? (
        <div className="flex flex-wrap items-end gap-4">
          <Field label={t`Unit`} htmlFor={`${idPrefix}-unit`} error={error}>
            <UnitPicker
              ctx={ctx}
              id={`${idPrefix}-unit`}
              value={value.unitId}
              onChange={(unitId) => onChange({ ...value, unitId })}
            />
          </Field>
          <Field label={t`Side`}>
            <SidePicker value={value.side} onChange={(side) => onChange({ ...value, side })} />
          </Field>
        </div>
      ) : (
        <Field label={t`Person`} htmlFor={`${idPrefix}-person`} error={error}>
          <PersonPicker
            ctx={ctx}
            id={`${idPrefix}-person`}
            value={value.personId}
            onChange={(personId) => onChange({ mode: 'person', personId })}
          />
        </Field>
      )}
    </div>
  )
}
