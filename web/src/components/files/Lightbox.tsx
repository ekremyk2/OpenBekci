import { useEffect } from 'react'
import { ChevronLeftIcon, ChevronRightIcon } from 'lucide-react'
import { useLingui } from '@lingui/react/macro'
import { fileUrl } from '@/api/files'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'

export interface LightboxImage {
  fileId: string
  name?: string
}

/** Image lightbox (Radix dialog, so focus is trapped and Escape closes). Loaded lazily. */
export default function Lightbox({
  orgId,
  images,
  index,
  onIndexChange,
  onClose,
}: {
  orgId: string
  images: readonly LightboxImage[]
  index: number
  onIndexChange: (i: number) => void
  onClose: () => void
}) {
  const { t } = useLingui()
  const current = images[index]

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === 'ArrowRight') onIndexChange(Math.min(images.length - 1, index + 1))
      if (e.key === 'ArrowLeft') onIndexChange(Math.max(0, index - 1))
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [images.length, index, onIndexChange])

  if (!current) return null
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-4xl">
        <DialogTitle className="truncate">{current.name ?? t`Photo`}</DialogTitle>
        <DialogDescription className="sr-only">
          {t`Photo ${index + 1} of ${images.length}`}
        </DialogDescription>
        <div className="flex items-center gap-2">
          <Button
            type="button"
            variant="ghost"
            size="icon"
            disabled={index === 0}
            onClick={() => onIndexChange(index - 1)}
            aria-label={t`Previous photo`}
          >
            <ChevronLeftIcon className="rtl-flip" />
          </Button>
          <img
            src={fileUrl(orgId, current.fileId, 'medium')}
            alt={current.name ?? ''}
            className="max-h-[70dvh] min-w-0 flex-1 object-contain"
          />
          <Button
            type="button"
            variant="ghost"
            size="icon"
            disabled={index === images.length - 1}
            onClick={() => onIndexChange(index + 1)}
            aria-label={t`Next photo`}
          >
            <ChevronRightIcon className="rtl-flip" />
          </Button>
        </div>
        <a
          href={fileUrl(orgId, current.fileId)}
          download
          className="text-sm text-primary underline-offset-4 hover:underline"
        >
          {t`Download original`}
        </a>
      </DialogContent>
    </Dialog>
  )
}
