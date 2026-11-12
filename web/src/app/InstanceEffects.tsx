import { useEffect } from 'react'
import { useInstance } from './instance'
import { applyBranding } from './theme'

const DEFAULT_NAME = 'OpenBekci'

/** Applies the discovery document to the page: brand seed colour and document title (page h1 · instance name). */
export function InstanceEffects() {
  const { data } = useInstance()
  const color = data?.branding?.primary_color
  const name = data?.instance_name

  useEffect(() => {
    applyBranding(color)
  }, [color])

  // Title = the page's h1 + instance name. shortcut: read from the DOM, not per-route meta, so all
  // ~80 routes (and tr/en) get one without annotating each; upgrade if a title must differ from the h1.
  useEffect(() => {
    const base = name || DEFAULT_NAME
    const sync = () => {
      const h1 = document.querySelector('main h1, [data-page-title]')?.textContent?.trim()
      document.title = h1 && h1 !== base ? `${h1} · ${base}` : base
    }
    sync()
    const observer = new MutationObserver(sync)
    observer.observe(document.body, { childList: true, subtree: true, characterData: true })
    return () => observer.disconnect()
  }, [name])

  return null
}

/** Instance display name, falling back to the product name until discovery has loaded. */
export function useInstanceName(): string {
  return useInstance().data?.instance_name || DEFAULT_NAME
}
