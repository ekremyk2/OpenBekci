import * as React from 'react'
import { useLingui } from '@lingui/react/macro'
import { cn } from '@/lib/utils'

function Table({ className, ...props }: React.ComponentProps<'table'>) {
  return (
    // scrollable region must be keyboard-focusable
    // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex
    <div className="relative max-h-[75vh] w-full overflow-auto" tabIndex={0}>
      <table className={cn('w-full caption-bottom text-sm', className)} {...props} />
    </div>
  )
}

function TableHeader({ className, ...props }: React.ComponentProps<'thead'>) {
  return <thead className={cn('[&_tr]:border-b', className)} {...props} />
}

function TableBody({ className, ...props }: React.ComponentProps<'tbody'>) {
  return <tbody className={cn('[&_tr:last-child]:border-0', className)} {...props} />
}

function TableRow({ className, ...props }: React.ComponentProps<'tr'>) {
  return <tr className={cn('border-b transition-colors hover:bg-muted/50', className)} {...props} />
}

function TableHead({ className, children, ...props }: React.ComponentProps<'th'>) {
  const { t } = useLingui()
  return (
    <th
      className={cn(
        'sticky top-0 z-10 h-11 bg-background px-2 text-start align-middle font-medium whitespace-nowrap text-muted-foreground',
        className,
      )}
      {...props}
    >
      {children ?? <span className="sr-only">{t`Actions`}</span>}
    </th>
  )
}

function TableCell({ className, ...props }: React.ComponentProps<'td'>) {
  return <td className={cn('p-2 align-middle', className)} {...props} />
}

function TableCaption({ className, ...props }: React.ComponentProps<'caption'>) {
  return <caption className={cn('mt-4 text-sm text-muted-foreground', className)} {...props} />
}

function TableFooter({ className, ...props }: React.ComponentProps<'tfoot'>) {
  return (
    <tfoot
      className={cn('border-t bg-muted/50 font-medium [&>tr]:last:border-b-0', className)}
      {...props}
    />
  )
}

export { Table, TableHeader, TableBody, TableFooter, TableRow, TableHead, TableCell, TableCaption }
