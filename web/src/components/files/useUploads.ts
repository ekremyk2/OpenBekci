import { useCallback, useEffect, useRef, useState } from 'react'
import { uploadFile, type Uploader } from '@/api/files'
import type { ApiFile } from '@/api/draft'
import { checkFile, type UploadProfile, type UploadRejection } from './limits'
import { downscaleImage } from './image'

export type UploadStatus = 'queued' | 'uploading' | 'done' | 'error' | 'cancelled' | 'rejected'

export interface UploadItem {
  id: string
  name: string
  size: number
  mime: string
  status: UploadStatus
  /** 0..1 */
  progress: number
  file?: ApiFile
  rejection?: UploadRejection
  error?: unknown
}

export interface UseUploadsOptions {
  orgId: string
  siteId?: string
  scope?: string
  profile?: UploadProfile
  /** Total files the controller accepts (rejected and cancelled ones do not count). */
  maxFiles?: number
  /** Parallel uploads. */
  concurrency?: number
  /** Downscale images to <= 2000 px before upload (ticket photos). */
  downscale?: boolean
  /** Test seam; defaults to the XHR transport. */
  uploader?: Uploader
}

export interface UploadsController {
  items: UploadItem[]
  /** Server files of finished uploads, in the order they were added. */
  files: ApiFile[]
  busy: boolean
  limitReached: boolean
  profile: UploadProfile
  addFiles: (files: Iterable<File>) => void
  cancel: (id: string) => void
  retry: (id: string) => void
  /** Drops an item from the list (cancels it if it is still running). */
  remove: (id: string) => void
  clear: () => void
}

/** Upload state machine behind `<UploadField>`: validation, a queue with parallelism, progress, cancel. */
export function useUploads(opts: UseUploadsOptions): UploadsController {
  const {
    orgId,
    siteId,
    scope,
    profile = 'any',
    maxFiles = Infinity,
    concurrency = 3,
    downscale = false,
    uploader = uploadFile,
  } = opts
  const [items, setItems] = useState<UploadItem[]>([])
  const sources = useRef(new Map<string, File>())
  const controllers = useRef(new Map<string, AbortController>())
  const started = useRef(new Set<string>())
  // one Idempotency-Key per file, kept across retries of that file
  const keys = useRef(new Map<string, string>())
  const alive = useRef(true)

  // Leaving the page cancels what is still running: no orphan files, no state updates afterwards.
  useEffect(() => {
    alive.current = true
    const running = controllers.current
    return () => {
      alive.current = false
      for (const c of running.values()) c.abort()
    }
  }, [])

  const patch = useCallback((id: string, change: Partial<UploadItem>) => {
    if (!alive.current) return
    setItems((prev) => prev.map((i) => (i.id === id ? { ...i, ...change } : i)))
  }, [])

  const counted = items.filter((i) => i.status !== 'rejected' && i.status !== 'cancelled')
  const limitReached = counted.length >= maxFiles

  const addFiles = useCallback(
    (incoming: Iterable<File>) => {
      setItems((prev) => {
        let room =
          maxFiles - prev.filter((i) => i.status !== 'rejected' && i.status !== 'cancelled').length
        const next = [...prev]
        for (const file of incoming) {
          const id = crypto.randomUUID()
          const base = { id, name: file.name, size: file.size, mime: file.type, progress: 0 }
          const rejection = checkFile(file, profile) ?? (room <= 0 ? 'too_many' : null)
          if (rejection) {
            next.push({ ...base, status: 'rejected', rejection })
            continue
          }
          room -= 1
          sources.current.set(id, file)
          keys.current.set(id, crypto.randomUUID())
          next.push({ ...base, status: 'queued' })
        }
        return next
      })
    },
    [maxFiles, profile],
  )

  const run = useCallback(
    async (id: string) => {
      const source = sources.current.get(id)
      if (!source) return
      const controller = new AbortController()
      controllers.current.set(id, controller)
      patch(id, { status: 'uploading', progress: 0, error: undefined })
      try {
        const body = downscale ? await downscaleImage(source) : source
        controller.signal.throwIfAborted() // the downscale cannot be interrupted, only discarded
        const file = await uploader({
          orgId,
          siteId,
          scope,
          idempotencyKey: keys.current.get(id),
          file: body,
          filename: source.name,
          signal: controller.signal,
          onProgress: (loaded, total) => patch(id, { progress: total > 0 ? loaded / total : 0 }),
        })
        patch(id, { status: 'done', progress: 1, file })
      } catch (e) {
        if (controller.signal.aborted || (e instanceof DOMException && e.name === 'AbortError')) {
          patch(id, { status: 'cancelled' })
        } else {
          patch(id, { status: 'error', error: e })
        }
      } finally {
        controllers.current.delete(id)
      }
    },
    [downscale, orgId, patch, scope, siteId, uploader],
  )

  // Start queued items while fewer than `concurrency` are running.
  useEffect(() => {
    let running = items.filter((i) => i.status === 'uploading').length
    for (const item of items) {
      if (running >= concurrency) break
      if (item.status === 'queued' && !started.current.has(item.id)) {
        started.current.add(item.id)
        running += 1
        void run(item.id)
      }
    }
  }, [items, concurrency, run])

  const cancel = useCallback(
    (id: string) => {
      const c = controllers.current.get(id)
      if (c) c.abort()
      else patch(id, { status: 'cancelled' })
    },
    [patch],
  )

  const retry = useCallback(
    (id: string) => {
      started.current.delete(id)
      patch(id, { status: 'queued', progress: 0, error: undefined })
    },
    [patch],
  )

  const remove = useCallback((id: string) => {
    controllers.current.get(id)?.abort()
    sources.current.delete(id)
    keys.current.delete(id)
    setItems((prev) => prev.filter((i) => i.id !== id))
  }, [])

  const clear = useCallback(() => {
    for (const c of controllers.current.values()) c.abort()
    sources.current.clear()
    keys.current.clear()
    started.current.clear()
    setItems([])
  }, [])

  const files = items.flatMap((i) => (i.status === 'done' && i.file ? [i.file] : []))
  return {
    items,
    files,
    busy: items.some((i) => i.status === 'queued' || i.status === 'uploading'),
    limitReached,
    profile,
    addFiles,
    cancel,
    retry,
    remove,
    clear,
  }
}
