import { appErrorFromResponse } from './errors'
import { emitApiEvent } from './events'
import { getLocaleProvider } from './client'
import type { ApiFile } from './draft'

/*
 * File transport. Uploads are multipart through the server only (backend §13): there are no
 * presigned URLs, so no storage origin ever reaches the browser. XMLHttpRequest is used because
 * `fetch` cannot report upload progress; it is kept here, with the rest of the transport.
 */

export type FileVariant = 'thumb' | 'medium'

/** URL of the content endpoint (`operations.downloadFile`); the server streams or redirects. */
export function fileUrl(orgId: string, fileId: string, variant?: FileVariant): string {
  const base = `/v1/orgs/${encodeURIComponent(orgId)}/files/${encodeURIComponent(fileId)}`
  return variant ? `${base}?variant=${variant}` : base
}

export interface UploadRequest {
  orgId: string
  file: Blob
  /** Name sent with the part (a downscaled Blob has none of its own). */
  filename: string
  siteId?: string
  scope?: string
  onProgress?: (loaded: number, total: number) => void
  signal?: AbortSignal
}

export type Uploader = (request: UploadRequest) => Promise<ApiFile>

/** `POST /v1/orgs/{org_id}/files` (operations.uploadFile). */
export const uploadFile: Uploader = (req) =>
  new Promise<ApiFile>((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('POST', `/v1/orgs/${encodeURIComponent(req.orgId)}/files`)
    xhr.withCredentials = true
    xhr.setRequestHeader('Accept', 'application/json')
    xhr.setRequestHeader('Accept-Language', getLocaleProvider()())
    xhr.setRequestHeader('X-Requested-With', 'openbekci-web')
    xhr.setRequestHeader('Idempotency-Key', crypto.randomUUID())

    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) req.onProgress?.(e.loaded, e.total)
    }
    xhr.onload = () => {
      let body: unknown
      try {
        body = JSON.parse(xhr.responseText)
      } catch {
        body = undefined
      }
      if (xhr.status >= 200 && xhr.status < 300) {
        req.onProgress?.(req.file.size, req.file.size)
        resolve(body as ApiFile)
        return
      }
      const error = appErrorFromResponse(
        new Response(null, { status: xhr.status, headers: retryAfter(xhr) }),
        body,
      )
      if (error.isUnauthorized) emitApiEvent({ type: 'unauthorized', error })
      reject(error)
    }
    xhr.onerror = () => reject(new TypeError('Network error'))
    xhr.onabort = () => reject(new DOMException('Upload cancelled', 'AbortError'))

    if (req.signal) {
      if (req.signal.aborted) {
        reject(new DOMException('Upload cancelled', 'AbortError'))
        return
      }
      req.signal.addEventListener('abort', () => xhr.abort(), { once: true })
    }

    const form = new FormData()
    form.append('file', req.file, req.filename)
    if (req.siteId) form.append('site_id', req.siteId)
    if (req.scope) form.append('scope', req.scope)
    xhr.send(form)
  })

function retryAfter(xhr: XMLHttpRequest): Headers {
  const h = new Headers()
  const v = xhr.getResponseHeader('Retry-After')
  if (v) h.set('Retry-After', v)
  return h
}
