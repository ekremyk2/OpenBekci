import * as React from 'react'
import { cn } from '@/lib/utils'

/** Native select styled like Input: keyboard and screen-reader behaviour for free. */
function Select({ className, ...props }: React.ComponentProps<'select'>) {
  return (
    <select
      className={cn(
        'flex h-11 w-full min-w-0 md:h-9 rounded-md border border-input bg-background px-3 py-1 text-base outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive md:text-sm',
        className,
      )}
      {...props}
    />
  )
}

export { Select }
