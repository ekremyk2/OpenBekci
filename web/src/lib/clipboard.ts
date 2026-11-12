/** Copies text; false when the browser refuses (no permission, insecure context) instead of an unhandled rejection. */
export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    return false
  }
}
