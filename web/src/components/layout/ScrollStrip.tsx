import { useCallback, useEffect, useRef, useState } from 'react'
import { ChevronRightIcon } from 'lucide-react'
import { useLingui } from '@lingui/react/macro'
import { cn } from '@/lib/utils'

/** A horizontally scrolling strip that fades out and shows a scroll button on each edge that has more to scroll to. */
export function ScrollStrip({
  label,
  children,
  className,
}: {
  label: string
  children: React.ReactNode
  className?: string
}) {
  const { t } = useLingui()
  const ref = useRef<HTMLElement>(null)
  const [edge, setEdge] = useState({ start: false, end: false })
  const update = useCallback(() => {
    const el = ref.current
    if (!el) return
    const x = Math.abs(el.scrollLeft) // negative in right-to-left
    setEdge({ start: x > 4, end: x < el.scrollWidth - el.clientWidth - 4 })
  }, [])
  useEffect(() => {
    update()
    const el = ref.current
    if (!el || typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(update)
    ro.observe(el)
    return () => ro.disconnect()
  }, [update, children])
  const scroll = (dir: 1 | -1) => {
    const el = ref.current
    if (!el) return
    const rtl = getComputedStyle(el).direction === 'rtl'
    el.scrollBy({ left: (rtl ? -dir : dir) * el.clientWidth * 0.8, behavior: 'smooth' })
  }
  const cue =
    'absolute inset-y-0 flex w-10 items-center from-background to-transparent text-muted-foreground hover:text-foreground'
  return (
    <div className="relative">
      <nav
        ref={ref}
        aria-label={label}
        onScroll={update}
        className={cn(
          '-mx-1 flex snap-x gap-1 overflow-x-auto px-1 pb-1 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden',
          className,
        )}
      >
        {children}
      </nav>
      {edge.start ? (
        <button
          type="button"
          aria-label={t`Scroll left`}
          onClick={() => scroll(-1)}
          className={cn(cue, 'start-0 justify-start bg-linear-to-r rtl:bg-linear-to-l')}
        >
          <ChevronRightIcon className="size-4 rotate-180 rtl:rotate-0" />
        </button>
      ) : null}
      {edge.end ? (
        <button
          type="button"
          aria-label={t`Scroll right`}
          onClick={() => scroll(1)}
          className={cn(cue, 'end-0 justify-end bg-linear-to-l rtl:bg-linear-to-r')}
        >
          <ChevronRightIcon className="size-4 rtl:rotate-180" />
        </button>
      ) : null}
    </div>
  )
}
