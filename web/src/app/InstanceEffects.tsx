import { useEffect } from 'react'
import { useInstance } from './instance'
import { applyBranding } from './theme'

const DEFAULT_NAME = 'OpenBekci'

/** Applies the discovery document to the page: brand seed colour and document title. */
export function InstanceEffects() {
  const { data } = useInstance()
  const color = data?.branding?.primary_color
  const name = data?.instance_name

  useEffect(() => {
    applyBranding(color)
  }, [color])

  useEffect(() => {
    document.title = name || DEFAULT_NAME
  }, [name])

  return null
}

/** Instance display name, falling back to the product name until discovery has loaded. */
export function useInstanceName(): string {
  return useInstance().data?.instance_name || DEFAULT_NAME
}
