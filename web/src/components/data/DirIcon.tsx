import type { LucideIcon, LucideProps } from 'lucide-react'
import { cn } from '@/lib/utils'

/** Wraps a direction-sensitive icon (arrows, chevrons) so it mirrors in RTL. */
export function DirIcon({ icon: Icon, className, ...props }: LucideProps & { icon: LucideIcon }) {
  return <Icon aria-hidden="true" className={cn('rtl-flip', className)} {...props} />
}
