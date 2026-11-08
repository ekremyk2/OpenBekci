import { describe, expect, it } from 'vitest'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/setup'
import { mountApp, nodeFile } from '@/test/render'
import { db } from '@/test/handlers/db'
import { reportToCsv } from './ImportWizardPage'

const UNITS = 'Daire No,Blok,Alan,Tür\nC-1,A Blok,80,flat\nC-2,A Blok,85,flat\n'

async function upload(csv: string, name = 'units.csv') {
  await userEvent.upload(await screen.findByLabelText('Spreadsheet file'), nodeFile([csv], name))
}

describe('import wizard', () => {
  it('maps columns, dry-runs, confirms, follows the job and imports the units', async () => {
    await mountApp('/orgs/org-1/import?kind=units')
    await upload(UNITS)
    // columns are guessed from the headers
    const door = await screen.findByLabelText(/Door number/)
    expect(door).toHaveValue('Daire No')
    await userEvent.click(screen.getByRole('button', { name: 'Check the file' }))
    expect(await screen.findByText('2 fine')).toBeInTheDocument()
    expect(screen.getByText('0 errors')).toBeInTheDocument()
    expect(db.units.some((u) => u.door_number === 'C-1')).toBe(false) // nothing saved by the dry run

    await userEvent.click(screen.getByRole('button', { name: 'Import 2 rows' }))
    expect(await screen.findByText('Import finished', {}, { timeout: 5000 })).toBeInTheDocument()
    expect(db.units.filter((u) => ['C-1', 'C-2'].includes(u.door_number))).toHaveLength(2)
  })

  it('shows row-level errors, blocks the import and offers the report as a download', async () => {
    await mountApp('/orgs/org-1/import?kind=units')
    await upload('Daire No,Alan\nA-1,90\n,50\nD-4,abc\n')
    await userEvent.click(await screen.findByRole('button', { name: 'Check the file' }))
    expect(await screen.findByText('3 errors')).toBeInTheDocument()
    const table = screen.getByRole('table', { name: 'Row-level report' })
    expect(within(table).getByText(/Door number is required/)).toBeInTheDocument()
    expect(within(table).getByText(/Area is not a number/)).toBeInTheDocument()
    expect(within(table).getByText(/already exists/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Import \d+ rows/ })).toBeDisabled()
    const link = screen.getByRole('link', { name: 'Download the report' })
    expect(link).toHaveAttribute('download', 'import-report.csv')
    expect(decodeURIComponent(link.getAttribute('href')!)).toContain('door_number,required')
  })

  it('remembers the column mapping per organization', async () => {
    await mountApp('/orgs/org-1/import?kind=units')
    await upload('Kapı,Ne\nX-1,1\n')
    const select = await screen.findByLabelText(/Door number/)
    await userEvent.selectOptions(select, 'Kapı')
    await userEvent.click(screen.getByRole('button', { name: 'Check the file' }))
    await screen.findByText('1 fine')
    expect(JSON.parse(localStorage.getItem('ob.importMapping.org-1.units')!)).toEqual({
      door_number: 'Kapı',
    })
  })

  it('imports opening balances with an as-of date and a required idempotency key', async () => {
    let key: string | null = null
    let asOf = ''
    server.use(
      http.post('*/v1/orgs/:orgId/opening-balances\\:import', async ({ request }) => {
        const text = await request.text()
        if (new URL(request.url).searchParams.get('dry_run') !== 'true') {
          key = request.headers.get('Idempotency-Key')
          asOf = /name="as_of"\r\n\r\n([^\r]+)/.exec(text)?.[1] ?? ''
          return HttpResponse.json(
            {
              dry_run: false,
              total_rows: 1,
              ok_rows: 1,
              warning_rows: 0,
              error_rows: 0,
              rows: [],
              rows_truncated: false,
              job_id: 'job-x',
            },
            { status: 202 },
          )
        }
        return HttpResponse.json({
          dry_run: true,
          total_rows: 1,
          ok_rows: 1,
          warning_rows: 0,
          error_rows: 0,
          rows: [],
          rows_truncated: false,
          job_id: null,
        })
      }),
      http.get('*/v1/orgs/:orgId/jobs/job-x', () =>
        HttpResponse.json({
          id: 'job-x',
          org_id: 'org-1',
          kind: 'import',
          status: 'succeeded',
          progress: 1,
          result_file_id: null,
          result: null,
          error: null,
          started_at: null,
          finished_at: null,
          created_by: null,
          created_at: '',
          updated_at: '',
        }),
      ),
    )
    await mountApp('/orgs/org-1/import?kind=opening_balances')
    expect(await screen.findByLabelText(/Balances as of/)).toBeInTheDocument()
    await upload('Daire,Malik Borç,Kiracı Borcu\nA-1,1200,300\n', 'opening.csv')
    expect(await screen.findByLabelText(/Owner: debt/)).toHaveValue('Malik Borç')
    await userEvent.click(screen.getByRole('button', { name: 'Check the file' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Import 1 rows' }))
    await screen.findByText('Import finished')
    expect(key).toBeTruthy()
    expect(asOf).toMatch(/^\d{4}-\d{2}-\d{2}$/)
  })

  it('reports an unreadable file', async () => {
    await mountApp('/orgs/org-1/import')
    await upload('', 'empty.csv')
    expect(await screen.findByText(/could not be read/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Check the file' })).not.toBeInTheDocument()
  })
})

describe('report csv', () => {
  it('has one line per error and one per clean row', () => {
    const csv = reportToCsv({
      dry_run: true,
      total_rows: 2,
      ok_rows: 1,
      warning_rows: 0,
      error_rows: 1,
      rows_truncated: false,
      job_id: null,
      rows: [
        {
          row: 2,
          status: 'error',
          errors: [
            { field: 'door_number', code: 'required', message: 'Door, number', params: null },
          ],
          data: {},
        },
        { row: 1, status: 'ok', errors: [], data: {} },
      ],
    })
    expect(csv.split('\r\n')).toEqual([
      'row,status,field,code,message',
      '2,error,door_number,required,"Door, number"',
      '1,ok,,,',
    ])
  })
})

describe('import wizard privacy and amounts', () => {
  it('masks the national id column in the preview', async () => {
    await mountApp('/orgs/org-1/import?kind=people')
    await upload('Ad Soyad,TCKN\nAli Veli,10000000146\n', 'people.csv')
    const table = await screen.findByRole('table', { name: 'First rows of the file' })
    expect(within(table).queryByText('10000000146')).not.toBeInTheDocument()
    expect(within(table).getByText('•••••••••46')).toBeInTheDocument()
    expect(within(table).getByText('Ali Veli')).toBeInTheDocument()
  })

  it('explains integer minor units and blocks decimal amount columns', async () => {
    await mountApp('/orgs/org-1/import?kind=opening_balances')
    await upload('Daire,Malik borc\nA-1,"1.234,50"\n', 'ob.csv')
    expect(await screen.findByText(/123450 means/)).toBeInTheDocument()
    expect(await screen.findByRole('alert')).toHaveTextContent('Malik borc')
    expect(screen.getByRole('button', { name: 'Check the file' })).toBeDisabled()
  })
})
