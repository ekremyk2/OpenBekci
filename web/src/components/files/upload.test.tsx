import { describe, expect, it } from 'vitest'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { act, render as rtlRender, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ApiFile } from '@/api/draft'
import type { UploadRequest, Uploader } from '@/api/files'
import { fileUrl } from '@/api/files'
import { acceptFor, checkFile, UPLOAD_LIMITS } from './limits'
import { UploadField } from './UploadField'
import { useUploads, type UseUploadsOptions } from './useUploads'

const fileMeta = (id: string, mime = 'image/png'): ApiFile =>
  ({
    id,
    mime,
    size: 1,
    org_id: 'org-1',
    status: 'ready',
    has_variants: true,
  }) as unknown as ApiFile

/** A controllable transport: every call parks until the test resolves, progresses or aborts it. */
function fakeUploader() {
  const calls: { req: UploadRequest; resolve: (f: ApiFile) => void }[] = []
  const uploader: Uploader = (req) =>
    new Promise<ApiFile>((resolve, reject) => {
      calls.push({ req, resolve })
      req.signal?.addEventListener('abort', () => reject(new DOMException('x', 'AbortError')))
    })
  return { uploader, calls }
}

const render = (ui: React.ReactElement) => rtlRender(<I18nProvider i18n={i18n}>{ui}</I18nProvider>)

function Harness(opts: Omit<UseUploadsOptions, 'orgId'>) {
  const controller = useUploads({ orgId: 'org-1', ...opts })
  return (
    <>
      <UploadField controller={controller} orgId="org-1" />
      <output data-testid="ids">{controller.files.map((f) => f.id).join(',')}</output>
      <output data-testid="busy">{String(controller.busy)}</output>
    </>
  )
}

const png = (name = 'a.png', size = 10) =>
  new File([new Uint8Array(size).fill(1)], name, { type: 'image/png' })

function choose(...files: File[]) {
  const input = document.querySelector('input[type=file]') as HTMLInputElement
  return userEvent.upload(input, files)
}

describe('limits', () => {
  it('checks size and mime like the server', () => {
    expect(checkFile({ name: 'a.png', size: 5, type: 'image/png' }, 'photo')).toBeNull()
    expect(checkFile({ name: 'a.png', size: 0, type: 'image/png' }, 'photo')).toBe('empty')
    expect(
      checkFile(
        { name: 'a.png', size: UPLOAD_LIMITS.photo.maxBytes + 1, type: 'image/png' },
        'photo',
      ),
    ).toBe('too_large')
    expect(checkFile({ name: 'a.pdf', size: 5, type: 'application/pdf' }, 'photo')).toBe(
      'type_not_allowed',
    )
    expect(checkFile({ name: 'a.pdf', size: 5, type: 'application/pdf' }, 'document')).toBeNull()
    // browsers sometimes report no type: fall back to the extension
    expect(checkFile({ name: 'scan.PDF', size: 5, type: '' }, 'document')).toBeNull()
    expect(checkFile({ name: 'run.exe', size: 5, type: '' }, 'document')).toBe('type_not_allowed')
    expect(acceptFor('any')).toBeUndefined()
    expect(acceptFor('photo')).toContain('image/jpeg')
  })

  it('builds content URLs through the API only', () => {
    expect(fileUrl('org-1', 'f1')).toBe('/v1/orgs/org-1/files/f1')
    expect(fileUrl('org-1', 'f1', 'thumb')).toBe('/v1/orgs/org-1/files/f1?variant=thumb')
  })
})

describe('UploadField', () => {
  it('shows progress, then done, and exposes the uploaded file', async () => {
    const { uploader, calls } = fakeUploader()
    render(<Harness uploader={uploader} profile="photo" />)
    await choose(png('wall.png'))
    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]!.req.filename).toBe('wall.png')
    expect(calls[0]!.req.orgId).toBe('org-1')

    act(() => calls[0]!.req.onProgress?.(25, 100))
    const bar = await screen.findByRole('progressbar', { name: 'Upload progress for wall.png' })
    expect(bar).toHaveAttribute('value', '25')
    expect(screen.getByTestId('busy')).toHaveTextContent('true')

    await act(async () => calls[0]!.resolve(fileMeta('server-1')))
    await waitFor(() => expect(screen.getByTestId('ids')).toHaveTextContent('server-1'))
    expect(screen.getByText('Uploaded')).toBeInTheDocument()
    expect(screen.getByTestId('busy')).toHaveTextContent('false')
  })

  it('rejects files that are too large or of the wrong type without uploading them', async () => {
    const { uploader, calls } = fakeUploader()
    render(<Harness uploader={uploader} profile="photo" />)
    const big = new File([new Uint8Array(1)], 'huge.png', { type: 'image/png' })
    Object.defineProperty(big, 'size', { value: UPLOAD_LIMITS.photo.maxBytes + 1 })
    // `accept` filters in a real picker; drop events bypass it, so use drop-like addFiles here
    const input = document.querySelector('input[type=file]') as HTMLInputElement
    await userEvent.upload(input, [big], { applyAccept: false })
    expect(await screen.findByText('The file is larger than 20.0 MB.')).toBeInTheDocument()

    await userEvent.upload(input, [new File(['x'], 'notes.pdf', { type: 'application/pdf' })], {
      applyAccept: false,
    })
    expect(await screen.findByText('This file type is not allowed.')).toBeInTheDocument()
    expect(calls).toHaveLength(0)
  })

  it('cancels a running upload and can retry it', async () => {
    const { uploader, calls } = fakeUploader()
    render(<Harness uploader={uploader} profile="photo" />)
    await choose(png('slow.png'))
    await waitFor(() => expect(calls).toHaveLength(1))
    await userEvent.click(screen.getByRole('button', { name: 'Cancel upload of slow.png' }))
    expect(await screen.findByText('Cancelled')).toBeInTheDocument()
    expect(calls[0]!.req.signal?.aborted).toBe(true)

    await userEvent.click(screen.getByRole('button', { name: 'Retry slow.png' }))
    await waitFor(() => expect(calls).toHaveLength(2))
    await act(async () => calls[1]!.resolve(fileMeta('server-2')))
    await waitFor(() => expect(screen.getByTestId('ids')).toHaveTextContent('server-2'))
  })

  it('uploads in parallel up to the concurrency limit and queues the rest', async () => {
    const { uploader, calls } = fakeUploader()
    render(<Harness uploader={uploader} profile="photo" concurrency={2} />)
    await choose(png('1.png'), png('2.png'), png('3.png'))
    await waitFor(() => expect(calls).toHaveLength(2))
    expect(screen.getByText('Waiting')).toBeInTheDocument()
    await act(async () => calls[0]!.resolve(fileMeta('s1')))
    await waitFor(() => expect(calls).toHaveLength(3))
  })

  it('reports a failed upload and offers a retry', async () => {
    const uploader: Uploader = () => Promise.reject(new Error('boom'))
    render(<Harness uploader={uploader} profile="photo" />)
    await choose(png('bad.png'))
    expect(await screen.findByText('Upload failed.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry bad.png' })).toBeInTheDocument()
  })

  it('enforces the maximum number of files', async () => {
    const { uploader, calls } = fakeUploader()
    render(<Harness uploader={uploader} profile="photo" maxFiles={1} />)
    const input = document.querySelector('input[type=file]') as HTMLInputElement
    await userEvent.upload(input, [png('1.png'), png('2.png')], { applyAccept: false })
    await waitFor(() => expect(calls).toHaveLength(1))
    expect(await screen.findByText('Too many files.')).toBeInTheDocument()
  })
})
