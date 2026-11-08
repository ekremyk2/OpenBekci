import * as React from 'react'
import * as ToastPrimitive from '@radix-ui/react-toast'
import { cva, type VariantProps } from 'class-variance-authority'
import { XIcon } from 'lucide-react'
import { cn } from '@/lib/utils'

const ToastProvider = ToastPrimitive.Provider

function ToastViewport({
  className,
  ...props
}: React.ComponentProps<typeof ToastPrimitive.Viewport>) {
  return (
    <ToastPrimitive.Viewport
      className={cn(
        'pointer-events-none fixed inset-x-0 bottom-0 z-[100] m-0 flex list-none flex-col items-center gap-2 p-4 outline-none',
        className,
      )}
      {...props}
    />
  )
}

const toastVariants = cva(
  'pointer-events-auto relative flex w-full max-w-sm items-start gap-3 rounded-md border border-s-4 bg-popover p-4 pe-10 text-sm text-popover-foreground shadow-lg data-[state=closed]:animate-out data-[state=closed]:fade-out-80 data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=open]:slide-in-from-bottom-2',
  {
    variants: {
      variant: {
        info: 'border-s-primary',
        warning: 'border-s-warning',
        error: 'border-s-destructive',
      },
    },
    defaultVariants: { variant: 'info' },
  },
)

function Toast({
  className,
  variant,
  ...props
}: React.ComponentProps<typeof ToastPrimitive.Root> & VariantProps<typeof toastVariants>) {
  return <ToastPrimitive.Root className={cn(toastVariants({ variant }), className)} {...props} />
}

function ToastDescription({
  className,
  ...props
}: React.ComponentProps<typeof ToastPrimitive.Description>) {
  return <ToastPrimitive.Description className={cn('flex-1', className)} {...props} />
}

function ToastClose({
  className,
  label,
  ...props
}: React.ComponentProps<typeof ToastPrimitive.Close> & { label: string }) {
  return (
    <ToastPrimitive.Close
      aria-label={label}
      className={cn(
        'absolute end-2 top-2 rounded-xs p-1 opacity-70 transition-opacity hover:opacity-100 focus-visible:ring-[3px] focus-visible:ring-ring focus-visible:outline-none [&_svg]:size-4',
        className,
      )}
      {...props}
    >
      <XIcon aria-hidden="true" />
    </ToastPrimitive.Close>
  )
}

export { ToastProvider, ToastViewport, Toast, ToastDescription, ToastClose }
