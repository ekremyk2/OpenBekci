import * as React from 'react'
import { Slot } from '@radix-ui/react-slot'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@/lib/utils'

// One pair of buttons: primary (default) and secondary; every variant has a hover step and a press.
const buttonVariants = cva(
  "inline-flex shrink-0 items-center justify-center gap-2 rounded-md text-sm font-medium whitespace-nowrap transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background active:translate-y-px disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg:not([class*='size-'])]:size-4 [&_svg]:shrink-0",
  {
    variants: {
      variant: {
        default:
          'bg-primary text-primary-foreground hover:bg-primary-hover active:bg-primary-hover',
        destructive:
          'bg-destructive text-destructive-foreground hover:bg-destructive/90 active:bg-destructive/80',
        outline:
          'border bg-background hover:bg-accent hover:text-accent-foreground active:bg-accent/70',
        secondary:
          'border border-input bg-transparent text-foreground hover:bg-accent active:bg-accent/70',
        ghost: 'hover:bg-accent hover:text-accent-foreground active:bg-accent/70',
        link: 'text-primary underline-offset-4 hover:underline',
      },
      // every size is a 44 px tap target at least
      size: {
        default: 'h-11 px-4 py-2',
        sm: 'h-11 gap-1.5 px-3',
        lg: 'h-12 px-6',
        // touch devices at the gate booth: 56 px
        xl: 'h-14 px-8 text-base',
        icon: 'size-11',
      },
    },
    defaultVariants: { variant: 'default', size: 'default' },
  },
)

function Button({
  className,
  variant,
  size,
  asChild = false,
  ...props
}: React.ComponentProps<'button'> & VariantProps<typeof buttonVariants> & { asChild?: boolean }) {
  const Comp = asChild ? Slot : 'button'
  return <Comp className={cn(buttonVariants({ variant, size, className }))} {...props} />
}

export { Button, buttonVariants }
