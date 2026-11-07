import { describe, expect, it } from 'vitest'
import type { Schema } from '@/api/draft'
import {
  guessMapping,
  importReducer,
  initialState,
  mappingStore,
  type ImportEvent,
  type ImportState,
} from './machine'
import { detectDelimiter, parseCsv, toCsv } from './parse'

const file = new File(['a'], 'units.csv')
const table = {
  headers: ['Daire', 'Blok', 'm2'],
  rows: [['A-1', 'A', '90']],
  delimiter: ',',
  sheet: null,
}
const report = (error_rows: number): Schema<'ImportReport'> => ({
  dry_run: true,
  total_rows: 3,
  ok_rows: 3 - error_rows,
  warning_rows: 0,
  error_rows,
  rows: [],
  rows_truncated: false,
  job_id: null,
})

function run(
  events: ImportEvent[],
  from: ImportState = initialState,
  kind: 'units' | 'people' = 'units',
) {
  const reduce = importReducer(kind)
  return events.reduce(reduce, from)
}

describe('import wizard state machine', () => {
  const parsed: ImportEvent = {
    type: 'FILE_PARSED',
    file,
    table,
    mapping: { door_number: 'Daire' },
  }

  it('walks upload → mapping → checking → report → committing → progress → done', () => {
    const s = run([
      parsed,
      { type: 'CHECK' },
      { type: 'CHECKED', report: report(0) },
      { type: 'CONFIRM' },
      { type: 'COMMITTED', jobId: 'job-1' },
      { type: 'JOB_FINISHED', ok: true },
    ])
    expect(s).toEqual({ step: 'done', jobId: 'job-1', ok: true })
  })

  it('will not check until every required field is mapped', () => {
    const s = run([{ ...parsed, mapping: {} } as ImportEvent, { type: 'CHECK' }])
    expect(s.step).toBe('mapping')
    const ok = run([
      { ...parsed, mapping: {} } as ImportEvent,
      { type: 'MAP', target: 'door_number', source: 'Daire' },
      { type: 'CHECK' },
    ])
    expect(ok.step).toBe('checking')
  })

  it('blocks the import while the report has errors', () => {
    const s = run([
      parsed,
      { type: 'CHECK' },
      { type: 'CHECKED', report: report(2) },
      { type: 'CONFIRM' },
    ])
    expect(s.step).toBe('report')
  })

  it('returns to mapping after a failed check and to the report after a failed commit', () => {
    expect(run([parsed, { type: 'CHECK' }, { type: 'CHECK_FAILED', error: 'x' }]).step).toBe(
      'mapping',
    )
    expect(
      run([
        parsed,
        { type: 'CHECK' },
        { type: 'CHECKED', report: report(0) },
        { type: 'CONFIRM' },
        { type: 'COMMIT_FAILED' },
      ]).step,
    ).toBe('report')
  })

  it('goes back from the report to the mapping, keeping the mapping', () => {
    const s = run([
      parsed,
      { type: 'CHECK' },
      { type: 'CHECKED', report: report(1) },
      { type: 'BACK' },
    ])
    expect(s).toMatchObject({ step: 'mapping', mapping: { door_number: 'Daire' } })
  })

  it('ignores events that do not apply to the current step', () => {
    expect(run([{ type: 'CONFIRM' }, { type: 'COMMITTED', jobId: 'x' }]).step).toBe('upload')
    const done = run([
      parsed,
      { type: 'CHECK' },
      { type: 'CHECKED', report: report(0) },
      { type: 'CONFIRM' },
      { type: 'COMMITTED', jobId: 'j' },
      { type: 'JOB_FINISHED', ok: false },
    ])
    expect(run([{ type: 'CHECKED', report: report(0) }], done)).toBe(done)
  })

  it('RESET starts over; a bad file keeps the wizard on upload with a message', () => {
    expect(run([parsed, { type: 'RESET' }])).toEqual(initialState)
    expect(run([{ type: 'FILE_FAILED', error: 'nope' }])).toEqual({ step: 'upload', error: 'nope' })
  })
})

describe('column mapping', () => {
  it('guesses columns from Turkish and English headers', () => {
    expect(guessMapping('units', ['Daire No', 'Blok', 'Alan', 'Tapu No'])).toEqual({
      door_number: 'Daire No',
      block: 'Blok',
      area_m2: 'Alan',
      legal_ref: 'Tapu No',
    })
    expect(guessMapping('opening_balances', ['Daire', 'Malik Borç', 'Kiracı Borcu'])).toMatchObject(
      {
        door_number: 'Daire',
        owner_principal: 'Malik Borç',
        tenant_principal: 'Kiracı Borcu',
      },
    )
  })

  it('remembers the mapping per organization and kind, dropping columns the new file lacks', () => {
    localStorage.clear()
    mappingStore.save('org-1', 'units', { door_number: 'Daire', block: 'Blok' })
    expect(mappingStore.load('org-1', 'units', ['Daire', 'X'])).toEqual({ door_number: 'Daire' })
    expect(mappingStore.load('org-2', 'units', ['Daire'])).toEqual({})
    expect(mappingStore.load('org-1', 'people', ['Daire'])).toEqual({})
  })
})

describe('csv', () => {
  it('detects the delimiter and parses quotes', () => {
    expect(detectDelimiter('a;b;c\n1;2;3')).toBe(';')
    expect(parseCsv('a,b\n"x, y","say ""hi"""\n')).toEqual([
      ['a', 'b'],
      ['x, y', 'say "hi"'],
    ])
  })
  it('round-trips through toCsv', () => {
    const rows = [
      ['a', 'b,c'],
      ['1', 'q"r'],
    ]
    expect(parseCsv(toCsv(rows))).toEqual(rows)
  })
})
