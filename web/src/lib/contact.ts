/** Phone and email checks for invitation destinations. */

const EMAIL = /^[^@\s]+@[^@\s]+\.[^@\s]+$/

/** `+90 555 111 22 33` → `+905551112233`, or null when it is not an E.164 number. */
export function normalizeE164(text: string): string | null {
  const s = text.replace(/[\s()-]/g, '')
  return /^\+[1-9]\d{6,14}$/.test(s) ? s : null
}

export function isEmail(text: string): boolean {
  return EMAIL.test(text.trim())
}

/** The destination as it is sent (E.164 for SMS, trimmed for email), or null when invalid. */
export function normalizeDestination(channel: string, text: string): string | null {
  const v = text.trim()
  if (channel === 'email') return isEmail(v) ? v : null
  return normalizeE164(v)
}
