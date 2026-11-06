import { Toaster as Sonner, type ToasterProps } from 'sonner'
import { useTheme } from '@/app/theme'

/** Toast host. Sonner renders an `aria-live` region, so toasts are announced. */
function Toaster(props: ToasterProps) {
  const { resolved } = useTheme()
  return (
    <Sonner
      theme={resolved}
      className="toaster group"
      style={
        {
          '--normal-bg': 'var(--popover)',
          '--normal-text': 'var(--popover-foreground)',
          '--normal-border': 'var(--border)',
        } as React.CSSProperties
      }
      {...props}
    />
  )
}

export { Toaster }
