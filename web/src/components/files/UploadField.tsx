import { useId, useRef, useState } from 'react'
import { FileIcon, RotateCcwIcon, UploadIcon, XIcon } from 'lucide-react'
import { Trans, useLingui } from '@lingui/react/macro'
import { AppError } from '@/api/errors'
import { fileUrl } from '@/api/files'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { acceptFor, isImageMime, UPLOAD_LIMITS, type UploadRejection } from './limits'
import type { UploadItem, UploadsController } from './useUploads'

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} kB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

function useRejectionText() {
  const { t } = useLingui()
  return (r: UploadRejection, profile: keyof typeof UPLOAD_LIMITS) => {
    switch (r) {
      case 'empty':
        return t`The file is empty.`
      case 'too_large':
        return t`The file is larger than ${formatBytes(UPLOAD_LIMITS[profile].maxBytes)}.`
      case 'type_not_allowed':
        return t`This file type is not allowed.`
      case 'too_many':
        return t`Too many files.`
    }
  }
}

function Row({
  item,
  controller,
  orgId,
}: {
  item: UploadItem
  controller: UploadsController
  orgId: string
}) {
  const { t } = useLingui()
  const rejectionText = useRejectionText()
  const image = isImageMime(item.mime)
  const pct = Math.round(item.progress * 100)
  return (
    <li className="flex items-center gap-3 rounded-md border p-2 text-sm" data-status={item.status}>
      {item.status === 'done' && item.file && image ? (
        <img
          src={fileUrl(orgId, item.file.id, 'thumb')}
          alt=""
          className="size-10 rounded object-cover"
        />
      ) : (
        <FileIcon className="size-5 shrink-0 text-muted-foreground" aria-hidden="true" />
      )}
      <div className="min-w-0 flex-1">
        <p className="truncate font-medium">{item.name}</p>
        <p className="text-xs text-muted-foreground">
          {item.status === 'rejected' && item.rejection ? (
            <span role="alert" className="text-destructive">
              {rejectionText(item.rejection, controller.profile)}
            </span>
          ) : item.status === 'error' ? (
            <span role="alert" className="text-destructive">
              {item.error instanceof AppError && item.error.isPayloadTooLarge ? (
                rejectionText('too_large', controller.profile)
              ) : (
                <Trans>Upload failed.</Trans>
              )}
            </span>
          ) : item.status === 'cancelled' ? (
            <Trans>Cancelled</Trans>
          ) : item.status === 'done' ? (
            <Trans>Uploaded</Trans>
          ) : item.status === 'queued' ? (
            <Trans>Waiting</Trans>
          ) : (
            <span>{formatBytes(item.size)}</span>
          )}
        </p>
        {item.status === 'uploading' ? (
          <progress
            className="mt-1 h-1.5 w-full"
            max={100}
            value={pct}
            aria-label={t`Upload progress for ${item.name}`}
          />
        ) : null}
      </div>
      {item.status === 'uploading' || item.status === 'queued' ? (
        <Button
          type="button"
          variant="ghost"
          size="icon"
          onClick={() => controller.cancel(item.id)}
          aria-label={t`Cancel upload of ${item.name}`}
        >
          <XIcon />
        </Button>
      ) : (
        <>
          {item.status === 'error' || item.status === 'cancelled' ? (
            <Button
              type="button"
              variant="ghost"
              size="icon"
              onClick={() => controller.retry(item.id)}
              aria-label={t`Retry ${item.name}`}
            >
              <RotateCcwIcon />
            </Button>
          ) : null}
          <Button
            type="button"
            variant="ghost"
            size="icon"
            onClick={() => controller.remove(item.id)}
            aria-label={t`Remove ${item.name}`}
          >
            <XIcon />
          </Button>
        </>
      )}
    </li>
  )
}

/** Drop zone + file list for a `useUploads` controller. */
export function UploadField({
  controller,
  orgId,
  label,
  hint,
  className,
}: {
  controller: UploadsController
  orgId: string
  label?: React.ReactNode
  hint?: React.ReactNode
  className?: string
}) {
  const inputId = useId()
  const input = useRef<HTMLInputElement>(null)
  const [over, setOver] = useState(false)
  const accept = acceptFor(controller.profile)
  return (
    <div className={cn('flex flex-col gap-2', className)}>
      <div
        onDragOver={(e) => {
          e.preventDefault()
          setOver(true)
        }}
        onDragLeave={() => setOver(false)}
        onDrop={(e) => {
          e.preventDefault()
          setOver(false)
          controller.addFiles(Array.from(e.dataTransfer.files))
        }}
        className={cn(
          'flex flex-col items-center gap-2 rounded-lg border border-dashed p-4 text-center text-sm',
          over && 'border-primary bg-accent',
        )}
      >
        <UploadIcon className="size-5 text-muted-foreground" aria-hidden="true" />
        <label htmlFor={inputId} className="font-medium">
          {label ?? <Trans>Drop files here or choose files</Trans>}
        </label>
        {hint ? <p className="text-xs text-muted-foreground">{hint}</p> : null}
        <input
          ref={input}
          id={inputId}
          type="file"
          multiple
          accept={accept}
          className="sr-only"
          disabled={controller.limitReached}
          onChange={(e) => {
            controller.addFiles(Array.from(e.target.files ?? []))
            e.target.value = ''
          }}
        />
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={controller.limitReached}
          onClick={() => input.current?.click()}
        >
          <Trans>Choose files</Trans>
        </Button>
      </div>
      {controller.items.length > 0 ? (
        <ul className="flex flex-col gap-2" aria-live="polite">
          {controller.items.map((item) => (
            <Row key={item.id} item={item} controller={controller} orgId={orgId} />
          ))}
        </ul>
      ) : null}
    </div>
  )
}
