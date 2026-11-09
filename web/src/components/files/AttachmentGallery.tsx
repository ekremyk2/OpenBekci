import { lazy, Suspense, useState } from 'react'
import { DownloadIcon } from 'lucide-react'
import { useLingui } from '@lingui/react/macro'
import { fileUrl } from '@/api/files'
import { isImageMime } from './limits'

// The lightbox is only needed once someone opens a photo.
const Lightbox = lazy(() => import('./Lightbox'))

export interface GalleryItem {
  id: string
  fileId: string
  mime: string
  name?: string
}

/** Thumbnails that open a lightbox; non-image files become download links. */
export function AttachmentGallery({
  orgId,
  items,
}: {
  orgId: string
  items: readonly GalleryItem[]
}) {
  const { t } = useLingui()
  const [open, setOpen] = useState<number | null>(null)
  const images = items.filter((i) => isImageMime(i.mime))
  const others = items.filter((i) => !isImageMime(i.mime))
  if (items.length === 0) return null
  return (
    <div className="flex flex-wrap gap-2">
      {images.map((img, i) => (
        <button
          key={img.id}
          type="button"
          onClick={() => setOpen(i)}
          className="overflow-hidden rounded-md border outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
          aria-label={t`Open photo ${i + 1} of ${images.length}`}
        >
          <img
            src={fileUrl(orgId, img.fileId, 'thumb')}
            alt={img.name ?? ''}
            className="size-20 object-cover"
            loading="lazy"
          />
        </button>
      ))}
      {others.map((f) => (
        <a
          key={f.id}
          href={fileUrl(orgId, f.fileId)}
          download
          className="inline-flex items-center gap-2 rounded-md border px-3 py-2 text-sm hover:bg-accent"
        >
          <DownloadIcon className="size-4" aria-hidden="true" />
          {f.name ?? t`Download`}
        </a>
      ))}
      {open !== null ? (
        <Suspense fallback={null}>
          <Lightbox
            orgId={orgId}
            images={images.map((i) => ({ fileId: i.fileId, name: i.name }))}
            index={open}
            onIndexChange={setOpen}
            onClose={() => setOpen(null)}
          />
        </Suspense>
      ) : null}
    </div>
  )
}
