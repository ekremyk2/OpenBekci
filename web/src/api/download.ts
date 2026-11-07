/**
 * File downloads (PDF receipts and statements, CSV/XLSX reports). Lives in src/api because raw
 * `fetch` is only allowed here; it carries the same cookie and CSRF marker as the typed client.
 */
export async function downloadFile(url: string, filename: string): Promise<void> {
  const res = await fetch(url, {
    credentials: 'include',
    headers: { 'X-Requested-With': 'openbekci-web' },
  })
  if (!res.ok) throw new Error(`Download failed (${res.status})`)
  const blob = await res.blob()
  const href = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = href
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(href), 10_000)
}

/** Builds `/v1/...?...&format=csv` for the spec's `format` query parameter. */
export function exportUrl(
  path: string,
  query: Record<string, string | undefined>,
  format: 'csv' | 'xlsx' | 'pdf',
) {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(query)) if (v !== undefined && v !== '') q.set(k, v)
  q.set('format', format)
  return `${path}?${q.toString()}`
}
