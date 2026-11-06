import { useSyncExternalStore } from 'react'

export type ToastVariant = 'info' | 'warning' | 'error'

export interface ToastItem {
  id: number
  message: string
  variant: ToastVariant
}

/*
 * Tiny toast store. Rendering lives in components/ui/toaster.tsx (Radix Toast, styles from the
 * stylesheet only), so no runtime <style> injection happens and a strict `style-src` CSP holds.
 */
let items: ToastItem[] = []
let nextId = 1
const listeners = new Set<() => void>()

function emit() {
  for (const l of listeners) l()
}

function push(variant: ToastVariant, message: string) {
  items = [...items, { id: nextId++, message, variant }].slice(-3)
  emit()
}

export const toast = {
  info: (message: string) => push('info', message),
  warning: (message: string) => push('warning', message),
  error: (message: string) => push('error', message),
}

export function dismissToast(id: number) {
  items = items.filter((i) => i.id !== id)
  emit()
}

export function clearToasts() {
  items = []
  emit()
}

export function useToasts(): readonly ToastItem[] {
  return useSyncExternalStore(
    (cb) => {
      listeners.add(cb)
      return () => listeners.delete(cb)
    },
    () => items,
    () => items,
  )
}
