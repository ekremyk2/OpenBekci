import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Trans, useLingui } from '@lingui/react/macro'
import { draftApi, ifMatch, multipart, type Schema } from '@/api/draft'
import { runWithReauth } from '@/app/reauth'
import { useIdempotencyKey, withIdempotencyKey } from '@/lib/idempotency'
import { ErrorState } from '@/components/data/ErrorState'
import { TableSkeleton } from '@/components/data/Skeletons'
import { FormError } from '@/components/forms/FormBits'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { parseFile, type ParsedTable } from '@/features/import/parse'
import { unitQueries } from '@/features/units/queries'
import { keyQueries, parsePasted, parseWeight } from './keys'

/** Per-unit value grid of one allocation key: inline edit, paste from a spreadsheet, CSV upload. */
export function KeyValuesEditor({
  orgId,
  siteId,
  allocationKey,
  canEdit,
}: {
  orgId: string
  siteId: string
  allocationKey: Schema<'AllocationKey'>
  canEdit: boolean
}) {
  const { t } = useLingui()
  const queryClient = useQueryClient()
  const idem = useIdempotencyKey()
  const computed = allocationKey.kind === 'equal' || allocationKey.kind === 'area'
  const valuesQ = useQuery(keyQueries.values(orgId, allocationKey.id))
  const unitsQ = useQuery(unitQueries.list(orgId, { siteId }, 200))
  // appurtenances have no weight of their own: they ride with their flat
  const units = useMemo(
    () => (unitsQ.data?.items ?? []).filter((u) => !u.parent_unit_id && u.status === 'active'),
    [unitsQ.data],
  )
  const [draft, setDraft] = useState<Record<string, string>>({})
  const [dirty, setDirty] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)

  // Re-seed the grid whenever fresh server values arrive (adjusting state during render, not in an effect).
  const [syncedAt, setSyncedAt] = useState(0)
  if (valuesQ.data && valuesQ.dataUpdatedAt !== syncedAt) {
    setSyncedAt(valuesQ.dataUpdatedAt)
    setDraft(Object.fromEntries(valuesQ.data.items.map((v) => [v.unit_id, String(v.value)])))
    setDirty(false)
  }

  const parsed = units.map((u) => ({
    unit: u,
    text: draft[u.id] ?? '',
    n: parseWeight(draft[u.id] ?? ''),
  }))
  const invalid = parsed.filter((p) => p.text.trim() !== '' && (Number.isNaN(p.n) || p.n < 0))
  const missing = parsed.filter((p) => p.text.trim() === '')
  const total = parsed.reduce((s, p) => s + (Number.isNaN(p.n) ? 0 : p.n), 0)

  const set = (unitId: string, text: string) => {
    setDraft((d) => ({ ...d, [unitId]: text }))
    setDirty(true)
  }

  function onPaste(e: React.ClipboardEvent<HTMLInputElement>, index: number) {
    const rows = parsePasted(e.clipboardData.getData('text'))
    if (rows.length <= 1 && !rows[0]?.door) return // a single value: normal paste
    e.preventDefault()
    const next: Record<string, string> = {}
    rows.forEach((r, i) => {
      const target = r.door
        ? units.find((u) => u.door_number.toLowerCase() === r.door!.toLowerCase())
        : units[index + i]
      if (target) next[target.id] = r.value
    })
    setDraft((d) => ({ ...d, ...next }))
    setDirty(true)
    setNotice(t`Pasted ${Object.keys(next).length} values. Check them, then save.`)
  }

  const save = useMutation({
    mutationFn: () =>
      runWithReauth(() =>
        draftApi.PUT('/v1/orgs/{org_id}/allocation-keys/{key_id}/values', {
          params: {
            path: { org_id: orgId, key_id: allocationKey.id },
            header: ifMatch(allocationKey.version),
          },
          ...withIdempotencyKey(idem.key),
          body: {
            items: parsed
              .filter((p) => p.text.trim() !== '')
              .map((p) => ({ unit_id: p.unit.id, value: p.n, computed: false })),
          },
        }),
      ),
    onSuccess: async () => {
      idem.renew()
      setNotice(t`Saved.`)
      setDirty(false)
      await queryClient.invalidateQueries({
        queryKey: ['tenancy.listAllocationKeyValues', orgId, allocationKey.id],
      })
    },
  })

  if (valuesQ.isLoading || unitsQ.isLoading) return <TableSkeleton rows={6} columns={3} />
  if (valuesQ.error || unitsQ.error)
    return (
      <ErrorState
        error={valuesQ.error ?? unitsQ.error}
        onRetry={() => {
          void valuesQ.refetch()
          void unitsQ.refetch()
        }}
      />
    )

  const fmt = (n: number) =>
    new Intl.NumberFormat(undefined, { maximumFractionDigits: 4 }).format(n)
  return (
    <div className="grid gap-3">
      <p className="text-sm text-muted-foreground">
        {computed ? (
          allocationKey.kind === 'equal' ? (
            <Trans>Every unit counts the same. There is nothing to enter.</Trans>
          ) : (
            <Trans>Each unit counts by its floor area. Change areas on the unit pages.</Trans>
          )
        ) : allocationKey.kind === 'share' ? (
          <Trans>
            Enter each unit&apos;s land share (arsa payı) from the title deed. Only the proportions
            matter: 100 of 1000 or 10 of 100 are the same.
          </Trans>
        ) : (
          <Trans>Enter a weight for each unit, or upload a table with one row per unit.</Trans>
        )}
      </p>
      <div className="overflow-hidden rounded-md border">
        <Table aria-label={t`Values for ${allocationKey.name}`}>
          <TableHeader>
            <TableRow>
              <TableHead scope="col">
                <Trans>Unit</Trans>
              </TableHead>
              <TableHead scope="col">
                <Trans>Value</Trans>
              </TableHead>
              <TableHead scope="col" className="text-end">
                <Trans>Share</Trans>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {parsed.map((p, i) => {
              const bad = p.text.trim() !== '' && (Number.isNaN(p.n) || p.n < 0)
              const stored = computed
                ? valuesQ.data?.items.find((v) => v.unit_id === p.unit.id)?.value
                : undefined
              return (
                <TableRow key={p.unit.id}>
                  <TableCell className="font-medium">{p.unit.door_number}</TableCell>
                  <TableCell>
                    {computed ? (
                      <span className="tabular-nums">
                        {stored === undefined ? '–' : fmt(stored)}
                      </span>
                    ) : (
                      <Input
                        aria-label={t`Value for ${p.unit.door_number}`}
                        inputMode="decimal"
                        className="h-8 w-32 text-end tabular-nums"
                        disabled={!canEdit}
                        aria-invalid={bad || undefined}
                        value={p.text}
                        onChange={(e) => set(p.unit.id, e.target.value)}
                        onPaste={(e) => onPaste(e, i)}
                      />
                    )}
                    {bad ? (
                      <span role="alert" className="ms-2 text-xs text-destructive">
                        <Trans>Enter zero or a positive number</Trans>
                      </span>
                    ) : null}
                  </TableCell>
                  <TableCell className="text-end tabular-nums text-muted-foreground">
                    {total > 0 && !Number.isNaN(p.n) && p.text.trim() !== ''
                      ? `${((p.n / total) * 100).toFixed(2)}%`
                      : '–'}
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </div>
      <div className="flex flex-wrap items-center gap-3 text-sm">
        <span>
          <Trans>Total</Trans>:{' '}
          <span className="font-medium tabular-nums">
            {fmt(computed ? (valuesQ.data?.total ?? 0) : total)}
          </span>
        </span>
        {!computed && missing.length > 0 ? (
          <Badge variant="warning">
            <Trans>{missing.length} units have no value yet</Trans>
          </Badge>
        ) : null}
        {!computed && missing.length === 0 && invalid.length === 0 ? (
          <Badge variant="success">
            <Trans>Complete</Trans>
          </Badge>
        ) : null}
      </div>
      {computed || !canEdit ? null : (
        <div className="flex flex-wrap items-center gap-3">
          <Button
            onClick={() => save.mutate()}
            disabled={!dirty || invalid.length > 0 || save.isPending}
          >
            <Trans>Save values</Trans>
          </Button>
          <span role="status" className="text-sm text-muted-foreground">
            {notice}
          </span>
          {save.error ? (
            <FormError message={t`The values could not be saved. Check them and try again.`} />
          ) : null}
        </div>
      )}
      {!computed && canEdit ? (
        <TableUpload
          orgId={orgId}
          allocationKey={allocationKey}
          onDone={() =>
            void queryClient.invalidateQueries({
              queryKey: ['tenancy.listAllocationKeyValues', orgId, allocationKey.id],
            })
          }
        />
      ) : null}
    </div>
  )
}

/** "Upload a per-unit table": CSV or XLSX, dry run first, then apply (`values:import`). */
function TableUpload({
  orgId,
  allocationKey,
  onDone,
}: {
  orgId: string
  allocationKey: Schema<'AllocationKey'>
  onDone: () => void
}) {
  const { t } = useLingui()
  const idem = useIdempotencyKey()
  const [file, setFile] = useState<File | null>(null)
  const [table, setTable] = useState<ParsedTable | null>(null)
  const [cols, setCols] = useState({ door: '', value: '' })
  const [report, setReport] = useState<Schema<'ImportReport'> | null>(null)
  const [error, setError] = useState<string | null>(null)

  const run = useMutation({
    mutationFn: async (dry: boolean) => {
      const mapping = {
        columns: { door_number: cols.door, value: cols.value },
        date_format: null,
        delimiter: table?.delimiter ?? null,
        sheet: null,
        remember: false,
      }
      const res = await runWithReauth(() =>
        draftApi.POST('/v1/orgs/{org_id}/allocation-keys/{key_id}/values:import', {
          params: { path: { org_id: orgId, key_id: allocationKey.id }, query: { dry_run: dry } },
          body: {} as never,
          ...multipart({ file: file!, mapping }),
          ...withIdempotencyKey(idem.key),
        }),
      )
      return { dry, data: res.data! }
    },
    onSuccess: ({ dry, data }) => {
      setReport(data)
      setError(null)
      if (!dry) {
        idem.renew()
        onDone()
      }
    },
    onError: () => setError(t`The table could not be read. Check the columns and try again.`),
  })

  return (
    <details className="rounded-md border p-3">
      <summary className="cursor-pointer text-sm font-medium">
        <Trans>Upload a per-unit table</Trans>
      </summary>
      <div className="mt-3 grid gap-3">
        <input
          type="file"
          accept=".csv,.xlsx,text/csv"
          aria-label={t`Table file`}
          className="text-sm"
          onChange={async (e) => {
            const f = e.target.files?.[0] ?? null
            setFile(f)
            setReport(null)
            if (!f) return setTable(null)
            const parsedTable = await parseFile(f)
            setTable(parsedTable)
            setCols({ door: parsedTable.headers[0] ?? '', value: parsedTable.headers[1] ?? '' })
          }}
        />
        {table ? (
          <div className="flex flex-wrap items-end gap-3">
            <label className="grid gap-1 text-sm">
              <Trans>Unit column</Trans>
              <Select
                value={cols.door}
                onChange={(e) => setCols((c) => ({ ...c, door: e.target.value }))}
              >
                {table.headers.map((h) => (
                  <option key={h} value={h}>
                    {h}
                  </option>
                ))}
              </Select>
            </label>
            <label className="grid gap-1 text-sm">
              <Trans>Value column</Trans>
              <Select
                value={cols.value}
                onChange={(e) => setCols((c) => ({ ...c, value: e.target.value }))}
              >
                {table.headers.map((h) => (
                  <option key={h} value={h}>
                    {h}
                  </option>
                ))}
              </Select>
            </label>
            <Button variant="outline" onClick={() => run.mutate(true)} disabled={run.isPending}>
              <Trans>Check the table</Trans>
            </Button>
          </div>
        ) : null}
        {report ? (
          <div className="grid gap-2 text-sm" role="status">
            <p>
              {report.dry_run ? (
                <Trans>
                  {report.ok_rows} rows are fine, {report.error_rows} have errors.
                </Trans>
              ) : (
                <Trans>Values saved.</Trans>
              )}
            </p>
            {report.rows
              .filter((r) => r.status === 'error')
              .slice(0, 10)
              .map((r) => (
                <p key={r.row} className="text-destructive">
                  <Trans>Row {r.row}</Trans>: {r.errors.map((x) => x.message ?? x.code).join(', ')}
                </p>
              ))}
            {report.dry_run && report.error_rows === 0 ? (
              <div>
                <Button onClick={() => run.mutate(false)} disabled={run.isPending}>
                  <Trans>Apply the table</Trans>
                </Button>
              </div>
            ) : null}
          </div>
        ) : null}
        <FormError message={error ?? undefined} />
      </div>
    </details>
  )
}
