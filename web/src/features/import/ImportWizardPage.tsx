import { useEffect, useReducer, useState } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import { DownloadIcon, UploadIcon } from 'lucide-react'
import { draftApi, multipart, type Schema } from '@/api/draft'
import { AppError } from '@/api/errors'
import { useLocale } from '@/app/locale'
import { runWithReauth } from '@/app/reauth'
import { formatMoney } from '@/lib/money'
import { todayInZone } from '@/lib/dates'
import { useSession } from '@/app/session'
import { useIdempotencyKey, withIdempotencyKey } from '@/lib/idempotency'
import { FormError } from '@/components/forms/FormBits'
import { SitePicker } from '@/components/forms/SitePicker'
import { PageHeader } from '@/components/layout/PageHeader'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
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
import { useActiveSite } from '@/features/sites/useActiveSite'
import { SetupNav } from '@/features/units/UnitsPage'
import {
  guessMapping,
  importReducer,
  initialState,
  maskCell,
  nonIntegerMoneyColumns,
  mappingStore,
  piiColumns,
  requiredMapped,
  TARGETS,
  type ImportKind,
} from './machine'
import { parseFile, toCsv } from './parse'
import { JobProgress, useJob, isTerminal } from './useJob'

const KINDS: ImportKind[] = ['units', 'people', 'opening_balances']

async function postImport(
  orgId: string,
  kind: ImportKind,
  v: {
    siteId: string
    file: File
    mapping: Record<string, string>
    delimiter: string | null
    asOf: string
    dryRun: boolean
    key: string
  },
) {
  const columnMapping = {
    columns: v.mapping,
    date_format: null,
    delimiter: v.delimiter,
    sheet: null,
    remember: false,
  }
  const common = {
    params: { path: { org_id: orgId }, query: { dry_run: v.dryRun } },
    body: {} as never,
    ...withIdempotencyKey(v.key),
  }
  const required = {
    params: {
      path: { org_id: orgId },
      query: { dry_run: v.dryRun },
      header: { 'Idempotency-Key': v.key },
    },
    body: {} as never,
  }
  if (kind === 'units')
    return draftApi.POST('/v1/orgs/{org_id}/units:import', {
      ...common,
      ...multipart({ site_id: v.siteId, file: v.file, mapping: columnMapping }),
    })
  if (kind === 'people')
    return draftApi.POST('/v1/orgs/{org_id}/persons:import', {
      ...common,
      ...multipart({ site_id: v.siteId, file: v.file, mapping: columnMapping }),
    })
  return draftApi.POST('/v1/orgs/{org_id}/opening-balances:import', {
    ...required,
    ...multipart({ site_id: v.siteId, as_of: v.asOf, file: v.file, mapping: columnMapping }),
  })
}

/** Downloads the row-level report as CSV (generated in the browser from the server's JSON). */
export function reportToCsv(report: Schema<'ImportReport'>): string {
  return toCsv([
    ['row', 'status', 'field', 'code', 'message'],
    ...report.rows.flatMap((r) =>
      r.errors.length === 0
        ? [[r.row, r.status, '', '', '']]
        : r.errors.map((e) => [r.row, r.status, e.field ?? '', e.code, e.message ?? '']),
    ),
  ])
}

export function ImportWizardPage({ search }: { search: { kind?: ImportKind; site?: string } }) {
  const { org, can } = useSession()
  const { t } = useLingui()
  const orgId = org?.id ?? ''
  const { siteId, sites, setSite, timezone, currency } = useActiveSite(search.site)
  const { intl } = useLocale()
  const [kind, setKind] = useState<ImportKind>(search.kind ?? 'units')
  const [state, dispatch] = useReducer(importReducer(kind), initialState)
  const [asOf, setAsOf] = useState(() => todayInZone(timezone))
  const [error, setError] = useState<string | null>(null)
  const idem = useIdempotencyKey()
  const job = useJob(orgId, state.step === 'progress' ? state.jobId : null)

  useEffect(() => {
    if (state.step === 'progress' && isTerminal(job.data?.status)) {
      dispatch({ type: 'JOB_FINISHED', ok: job.data?.status === 'succeeded' })
    }
  }, [state, job.data?.status])

  const label = (k: ImportKind) =>
    k === 'units' ? t`Units` : k === 'people' ? t`People` : t`Opening balances`
  const title = (field: string) =>
    ({
      door_number: t`Door number`,
      block: t`Block`,
      type: t`Type`,
      area_m2: t`Area (m²)`,
      legal_ref: t`Title deed no.`,
      parent_door_number: t`Belongs to (door number)`,
      display_name: t`Name`,
      phone: t`Phone`,
      email: t`Email`,
      national_id: t`National id`,
      kind: t`Owner or tenant`,
      share_pct: t`Share %`,
      owner_principal: t`Owner: debt`,
      owner_late_fee: t`Owner: known late fee`,
      tenant_principal: t`Tenant: debt`,
      tenant_late_fee: t`Tenant: known late fee`,
    })[field] ?? field

  async function onFile(file: File | undefined) {
    if (!file) return
    try {
      const table = await parseFile(file)
      if (table.headers.length === 0) throw new Error('empty')
      const saved = mappingStore.load(orgId, kind, table.headers)
      dispatch({
        type: 'FILE_PARSED',
        file,
        table,
        mapping: { ...guessMapping(kind, table.headers), ...saved },
      })
      setError(null)
    } catch {
      dispatch({
        type: 'FILE_FAILED',
        error: t`This file could not be read. Use a CSV or XLSX file with a header row.`,
      })
    }
  }

  async function check() {
    if (state.step !== 'mapping' || !siteId) return
    dispatch({ type: 'CHECK' })
    mappingStore.save(orgId, kind, state.mapping)
    try {
      // a dry run gets its own key: it must never replay as the commit
      const { data } = await postImport(orgId, kind, {
        siteId,
        file: state.file,
        mapping: state.mapping,
        delimiter: state.table.delimiter,
        asOf,
        dryRun: true,
        key: crypto.randomUUID(),
      })
      dispatch({ type: 'CHECKED', report: data! })
    } catch (e) {
      setError(
        e instanceof AppError && e.status === 413
          ? t`This file is too large.`
          : t`The file could not be checked. Please try again.`,
      )
      dispatch({ type: 'CHECK_FAILED', error: '' })
    }
  }

  async function confirm() {
    if (state.step !== 'report' || !siteId) return
    dispatch({ type: 'CONFIRM' })
    try {
      const res = await runWithReauth(() =>
        postImport(orgId, kind, {
          siteId,
          file: state.file,
          mapping: state.mapping,
          delimiter: state.table.delimiter,
          asOf,
          dryRun: false,
          key: idem.key,
        }),
      )
      const jobId = res.data?.job_id
      if (!jobId) throw new Error('no job')
      idem.renew()
      dispatch({ type: 'COMMITTED', jobId })
    } catch {
      setError(
        t`The import could not be started. Please try again; a retry never applies it twice.`,
      )
      dispatch({ type: 'COMMIT_FAILED' })
    }
  }

  const badMoney =
    kind === 'opening_balances' && (state.step === 'mapping' || state.step === 'checking')
      ? nonIntegerMoneyColumns(state.table, state.mapping)
      : []
  const pii =
    state.step === 'mapping' || state.step === 'checking'
      ? piiColumns(state.table.headers, state.mapping)
      : new Set<string>()
  const exampleMinor = '123450'
  const exampleFormatted = formatMoney(123450, currency, intl)
  const permitted = kind === 'opening_balances' ? can('money.post') : can('units.import')
  const stepIndex = {
    upload: 1,
    mapping: 2,
    checking: 3,
    report: 3,
    committing: 4,
    progress: 4,
    done: 5,
  }[state.step]

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={<Trans>Import</Trans>}
        description={
          <Trans>
            Bring units, people and opening balances in from a spreadsheet. Nothing is saved until
            you have seen the check and confirmed.
          </Trans>
        }
      />
      <SetupNav />
      <div className="flex flex-wrap items-end gap-3">
        <SitePicker sites={sites} value={siteId} onChange={setSite} />
        <label className="flex items-center gap-2 text-sm">
          <Trans>What to import</Trans>
          <Select
            className="w-auto"
            value={kind}
            disabled={state.step !== 'upload'}
            onChange={(e) => setKind(e.target.value as ImportKind)}
          >
            {KINDS.map((k) => (
              <option key={k} value={k}>
                {label(k)}
              </option>
            ))}
          </Select>
        </label>
        {kind === 'opening_balances' ? (
          <label className="flex items-center gap-2 text-sm">
            <Trans>Balances as of</Trans>
            <Input
              type="date"
              className="w-auto"
              value={asOf}
              onChange={(e) => setAsOf(e.target.value)}
            />
          </label>
        ) : null}
      </div>
      <ol className="flex flex-wrap gap-2 text-sm" aria-label={t`Steps`}>
        {[t`Upload`, t`Match columns`, t`Check`, t`Import`, t`Result`].map((s, i) => (
          <li key={s} aria-current={stepIndex === i + 1 ? 'step' : undefined}>
            <Badge variant={stepIndex === i + 1 ? 'default' : 'outline'}>
              {i + 1}. {s}
            </Badge>
          </li>
        ))}
      </ol>

      {!permitted ? <FormError message={t`You do not have permission to import this.`} /> : null}

      {state.step === 'upload' && permitted ? (
        <div className="grid max-w-xl gap-3">
          <label className="flex cursor-pointer flex-col items-center gap-2 rounded-lg border border-dashed p-8 text-center focus-within:ring-2 focus-within:ring-ring focus-within:ring-offset-2">
            <UploadIcon className="size-6 text-muted-foreground" aria-hidden="true" />
            <span className="text-sm font-medium">
              <Trans>Choose a CSV or XLSX file</Trans>
            </span>
            <input
              type="file"
              accept=".csv,.xlsx,text/csv"
              aria-label={t`Spreadsheet file`}
              className="text-sm"
              onChange={(e) => void onFile(e.target.files?.[0])}
            />
          </label>
          <FormError message={state.error} />
        </div>
      ) : null}

      {state.step === 'mapping' || state.step === 'checking' ? (
        <div className="grid gap-4">
          <p className="text-sm text-muted-foreground">
            <Trans>
              Match each field to a column of your file. Your choices are remembered for next time.
            </Trans>
          </p>
          <div className="grid max-w-2xl gap-3 sm:grid-cols-2">
            {TARGETS[kind].map((f) => (
              <label key={f.id} className="grid gap-1 text-sm">
                <span>
                  {title(f.id)}
                  {f.required ? ' *' : ''}
                </span>
                <Select
                  value={state.mapping[f.id] ?? ''}
                  onChange={(e) =>
                    dispatch({ type: 'MAP', target: f.id, source: e.target.value || null })
                  }
                >
                  <option value="">{t`Not in my file`}</option>
                  {state.table.headers.map((h) => (
                    <option key={h} value={h}>
                      {h}
                    </option>
                  ))}
                </Select>
              </label>
            ))}
          </div>
          {kind === 'opening_balances' ? (
            <p className="text-sm text-muted-foreground">
              <Trans>
                Amounts are whole numbers in the smallest currency unit, without separators:{' '}
                {exampleMinor} means {exampleFormatted}.
              </Trans>
            </p>
          ) : null}
          {badMoney.length > 0 ? (
            <p role="alert" className="text-sm text-destructive">
              <Trans>
                These columns have values that are not whole numbers: {badMoney.join(', ')}. Write
                amounts in the smallest unit, for example {exampleMinor} for {exampleFormatted}.
              </Trans>
            </p>
          ) : null}
          <div className="overflow-x-auto rounded-md border">
            <Table aria-label={t`First rows of the file`}>
              <TableHeader>
                <TableRow>
                  {state.table.headers.map((h) => (
                    <TableHead key={h} scope="col">
                      {h}
                    </TableHead>
                  ))}
                </TableRow>
              </TableHeader>
              <TableBody>
                {state.table.rows.map((r, i) => (
                  <TableRow key={i}>
                    {state.table.headers.map((h, c) => (
                      <TableCell key={h}>{pii.has(h) ? maskCell(r[c]) : r[c]}</TableCell>
                    ))}
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
          <FormError message={error ?? undefined} />
          <div className="flex gap-2">
            <Button variant="outline" onClick={() => dispatch({ type: 'BACK' })}>
              <Trans>Choose another file</Trans>
            </Button>
            <Button
              onClick={() => void check()}
              disabled={
                !requiredMapped(kind, state.mapping) ||
                badMoney.length > 0 ||
                state.step === 'checking' ||
                !siteId
              }
            >
              <Trans>Check the file</Trans>
            </Button>
          </div>
        </div>
      ) : null}

      {state.step === 'report' || state.step === 'committing' ? (
        <ReportView
          report={state.report}
          busy={state.step === 'committing'}
          error={error}
          onBack={() => dispatch({ type: 'BACK' })}
          onConfirm={() => void confirm()}
        />
      ) : null}

      {state.step === 'progress' ? (
        <div className="grid max-w-xl gap-2">
          <h2 className="text-base font-semibold">
            <Trans>Importing…</Trans>
          </h2>
          <JobProgress orgId={orgId} jobId={state.jobId} />
        </div>
      ) : null}

      {state.step === 'done' ? (
        <div className="grid max-w-xl gap-3" role="status">
          <h2 className="text-base font-semibold">
            {state.ok ? <Trans>Import finished</Trans> : <Trans>Import failed</Trans>}
          </h2>
          <p className="text-sm text-muted-foreground">
            {state.ok ? (
              <Trans>The data is in. You can review it on the units and people pages.</Trans>
            ) : (
              <Trans>
                The import did not complete. Some rows may already have been applied: check the
                units and people pages, fix the file and try again.
              </Trans>
            )}
          </p>
          <div>
            <Button variant="outline" onClick={() => dispatch({ type: 'RESET' })}>
              <Trans>Import another file</Trans>
            </Button>
          </div>
        </div>
      ) : null}
    </div>
  )
}

function ReportView({
  report,
  busy,
  error,
  onBack,
  onConfirm,
}: {
  report: Schema<'ImportReport'>
  busy: boolean
  error: string | null
  onBack: () => void
  onConfirm: () => void
}) {
  const { t } = useLingui()
  const [onlyProblems, setOnlyProblems] = useState(true)
  const rows = onlyProblems ? report.rows.filter((r) => r.status !== 'ok') : report.rows
  const csvHref = `data:text/csv;charset=utf-8,${encodeURIComponent(reportToCsv(report))}`
  return (
    <div className="grid gap-3">
      <div className="flex flex-wrap items-center gap-3 text-sm" role="status">
        <span>
          <Trans>{report.total_rows} rows</Trans>
        </span>
        <Badge variant="success">
          <Trans>{report.ok_rows} fine</Trans>
        </Badge>
        <Badge variant="warning">
          <Trans>{report.warning_rows} warnings</Trans>
        </Badge>
        <Badge variant={report.error_rows > 0 ? 'destructive' : 'secondary'}>
          <Trans>{report.error_rows} errors</Trans>
        </Badge>
        <a
          href={csvHref}
          download="import-report.csv"
          className="ms-auto inline-flex items-center gap-1 text-sm underline-offset-4 hover:underline"
        >
          <DownloadIcon className="size-4" aria-hidden="true" />
          <Trans>Download the report</Trans>
        </a>
      </div>
      {report.rows_truncated ? (
        <p className="text-xs text-muted-foreground">
          <Trans>Only the first 1000 rows are listed, errors first.</Trans>
        </p>
      ) : null}
      <label className="flex items-center gap-2 text-sm">
        <Checkbox checked={onlyProblems} onChange={(e) => setOnlyProblems(e.target.checked)} />
        <Trans>Show only rows with problems</Trans>
      </label>
      {rows.length > 0 ? (
        <div className="max-h-96 overflow-auto rounded-md border">
          <Table aria-label={t`Row-level report`}>
            <TableHeader>
              <TableRow>
                <TableHead scope="col">
                  <Trans>Row</Trans>
                </TableHead>
                <TableHead scope="col">
                  <Trans>Result</Trans>
                </TableHead>
                <TableHead scope="col">
                  <Trans>What to fix</Trans>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((r) => (
                <TableRow key={r.row}>
                  <TableCell>{r.row}</TableCell>
                  <TableCell>
                    <Badge
                      variant={
                        r.status === 'error'
                          ? 'destructive'
                          : r.status === 'warning'
                            ? 'warning'
                            : 'success'
                      }
                    >
                      {r.status}
                    </Badge>
                  </TableCell>
                  <TableCell className="text-sm">
                    {r.errors
                      .map((e) => (e.field ? `${e.field}: ` : '') + (e.message ?? e.code))
                      .join('; ')}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      ) : (
        <p className="text-sm text-muted-foreground">
          <Trans>No problems found.</Trans>
        </p>
      )}
      <FormError message={error ?? undefined} />
      <div className="flex flex-wrap gap-2">
        <Button variant="outline" onClick={onBack}>
          <Trans>Back to columns</Trans>
        </Button>
        <Button onClick={onConfirm} disabled={report.error_rows > 0 || busy}>
          <Trans>Import {report.ok_rows + report.warning_rows} rows</Trans>
        </Button>
        {report.error_rows > 0 ? (
          <span className="self-center text-sm text-muted-foreground">
            <Trans>Fix the errors in your file and upload it again.</Trans>
          </span>
        ) : null}
      </div>
    </div>
  )
}
