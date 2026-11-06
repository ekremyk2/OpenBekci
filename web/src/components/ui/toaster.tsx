import { useLingui } from '@lingui/react/macro'
import { dismissToast, useToasts } from '@/lib/toast'
import {
  Toast,
  ToastClose,
  ToastDescription,
  ToastProvider,
  ToastViewport,
} from '@/components/ui/toast'

/** Toast host. Radix renders an `aria-live` region, so toasts are announced. */
export function Toaster() {
  const { t } = useLingui()
  const toasts = useToasts()
  return (
    <ToastProvider swipeDirection="down" duration={6000} label={t`Notifications`}>
      {toasts.map((item) => (
        <Toast
          key={item.id}
          variant={item.variant}
          type={item.variant === 'error' ? 'foreground' : 'background'}
          onOpenChange={(open) => {
            if (!open) dismissToast(item.id)
          }}
        >
          <ToastDescription>{item.message}</ToastDescription>
          <ToastClose label={t`Close`} />
        </Toast>
      ))}
      <ToastViewport />
    </ToastProvider>
  )
}
