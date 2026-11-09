import * as React from 'react'
import { cn } from '@/lib/utils'

/** Native checkbox, labelled by the surrounding `<label>`. */
function Checkbox({ className, ...props }: Omit<React.ComponentProps<'input'>, 'type'>) {
  return (
    <input
      type="checkbox"
      className={cn(
        'size-4 shrink-0 rounded-xs border border-input accent-primary outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:cursor-not-allowed disabled:opacity-50',
        className,
      )}
      {...props}
    />
  )
}

export { Checkbox }
