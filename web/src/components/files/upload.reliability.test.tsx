import { afterEach, describe, expect, it, vi } from 'vitest'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { act, render as rtlRender, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ApiFile } from '@/api/draft'
import { AppError } from '@/api/errors'
import type { UploadRequest, Uploader } from '@/api/files'
import { downscaleImage } from './image'
import { UploadField } from './UploadField'
import { useUploads, type UseUploadsOptions } from './useUploads'

const render = (ui: React.ReactElement) => rtlRender(<I18nProvider i18n={i18n}>{ui}</I18nProvider>)

function Harness(opts: Omit<UseUploadsOptions, 'orgId'>) {
  const controller = useUploads({ orgId: 'org-1', ...opts })
  return <UploadField controller={controller} orgId="org-1" />
}

const png = (name: string, size = 10) =>
  new File([new Uint8Array(size).fill(1)], name, { type: 'image/png' })

function choose(...files: File[]) {
  const input = document.querySelector('input[type=file]') as HTMLInputElement
  return userEvent.upload(input, files)
}

function parkingUploader() {
  const calls: {
    req: UploadRequest
    reject: (e: unknown) => void
    resolve: (f: ApiFile) => void
  }[] = []
  const uploader: Uploader = (req) =>
    new Promise<ApiFile>((resolve, reject) => {
      calls.push({ req, resolve, reject })
      req.signal?.addEventListener('abort', () => reject(new DOMException('x', 'AbortError')))
    })
  return { uploader, calls }
}

describe('upload lifecycle', () => {
  it('aborts running uploads when the page unmounts', async () => {
    const { uploader, calls } = parkingUploader()
    const view = render(<Harness uploader={uploader} profile="photo" />)
    await choose(png('a.png'), png('b.png'))
    await waitFor(() => expect(calls).toHaveLength(2))
    expect(calls.map((c) => c.req.signal?.aborted)).toEqual([false, false])
    view.unmount()
    expect(calls.map((c) => c.req.signal?.aborted)).toEqual([true, true])
  })

  it('keeps one Idempotency-Key per file across retries and gives each file its own', async () => {
    const { uploader, calls } = parkingUploader()
    render(<Harness uploader={uploader} profile="photo" />)
    await choose(png('a.png'), png('b.png'))
    await waitFor(() => expect(calls).toHaveLength(2))
    const [a, b] = calls
    expect(a!.req.idempotencyKey).toBeTruthy()
    expect(a!.req.idempotencyKey).not.toBe(b!.req.idempotencyKey)

    // the response to the first attempt is lost: the transport fails, the person retries
    await act(async () => a!.reject(new TypeError('Network error')))
    await userEvent.click(await screen.findByRole('button', { name: 'Retry a.png' }))
    await waitFor(() => expect(calls).toHaveLength(3))
    expect(calls[2]!.req.idempotencyKey).toBe(a!.req.idempotencyKey)

    // cancelling and retrying is the same series of attempts
    await userEvent.click(screen.getByRole('button', { name: 'Cancel upload of b.png' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Retry b.png' }))
    await waitFor(() => expect(calls).toHaveLength(4))
    expect(calls[3]!.req.idempotencyKey).toBe(b!.req.idempotencyKey)
  })

  it('shows the size limit when the server answers 413', async () => {
    const uploader: Uploader = () =>
      Promise.reject(new AppError({ status: 413, code: 'payload_too_large' }))
    render(<Harness uploader={uploader} profile="photo" />)
    await choose(png('big.png'))
    expect(await screen.findByText('The file is larger than 25.0 MB.')).toBeInTheDocument()
    expect(screen.queryByText('Upload failed.')).not.toBeInTheDocument()
  })
})

describe('downscaleImage', () => {
  const realCreate = globalThis.createImageBitmap
  const realGetContext = HTMLCanvasElement.prototype.getContext
  const realToBlob = HTMLCanvasElement.prototype.toBlob
  afterEach(() => {
    globalThis.createImageBitmap = realCreate
    HTMLCanvasElement.prototype.getContext = realGetContext
    HTMLCanvasElement.prototype.toBlob = realToBlob
  })

  function stubCanvas(blobSize: number) {
    const drawn: { w: number; h: number }[] = []
    HTMLCanvasElement.prototype.getContext = function (this: HTMLCanvasElement) {
      return {
        drawImage: (_b: unknown, _x: number, _y: number, w: number, h: number) =>
          drawn.push({ w, h }),
      }
    } as never
    HTMLCanvasElement.prototype.toBlob = function (cb: BlobCallback, type?: string) {
      cb(new Blob([new Uint8Array(blobSize)], { type }))
    } as never
    return drawn
  }
  const bitmap = (width: number, height: number) => {
    const close = vi.fn()
    globalThis.createImageBitmap = vi.fn(async () => ({ width, height, close })) as never
    return close
  }
  const photo = (type = 'image/jpeg', size = 5000) =>
    new File([new Uint8Array(size)], 'p.jpg', { type })

  it('scales the longer edge down to 2000 px and returns the smaller blob', async () => {
    const drawn = stubCanvas(1000)
    const close = bitmap(4000, 3000)
    const out = await downscaleImage(photo())
    expect(drawn).toEqual([{ w: 2000, h: 1500 }])
    expect(out).not.toBeInstanceOf(File)
    expect(out.size).toBe(1000)
    expect(out.type).toBe('image/jpeg')
    expect(close).toHaveBeenCalled()
  })

  it('leaves images that are already small, other files, and undecodable images alone', async () => {
    stubCanvas(1000)
    bitmap(1200, 800)
    const small = photo()
    expect(await downscaleImage(small)).toBe(small)

    const pdf = new File(['x'], 'a.pdf', { type: 'application/pdf' })
    expect(await downscaleImage(pdf)).toBe(pdf)

    globalThis.createImageBitmap = vi.fn(async () => {
      throw new Error('cannot decode HEIC')
    }) as never
    const heic = photo('image/heic')
    expect(await downscaleImage(heic)).toBe(heic)
  })

  it('keeps the original when the scaled copy is not smaller', async () => {
    stubCanvas(9000)
    bitmap(4000, 3000)
    const original = photo('image/png', 5000)
    expect(await downscaleImage(original)).toBe(original)
  })

  it('returns the file untouched where canvas is unavailable', async () => {
    globalThis.createImageBitmap = undefined as never
    const original = photo()
    expect(await downscaleImage(original)).toBe(original)
  })
})
