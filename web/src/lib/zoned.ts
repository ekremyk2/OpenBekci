/*
 * `<input type="datetime-local">` holds a wall-clock time with no zone. Site-scheduled things
 * (announcement publish/expiry) are wall-clock times in the SITE's timezone (web plan §3), so these
 * helpers convert between that wall-clock string and an instant, never using the browser's zone.
 */

function offsetMs(ts: number, timeZone: string): number {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone,
    hourCycle: 'h23',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).formatToParts(new Date(ts))
  const get = (type: string) => Number(parts.find((p) => p.type === type)?.value)
  const asUtc = Date.UTC(
    get('year'),
    get('month') - 1,
    get('day'),
    get('hour'),
    get('minute'),
    get('second'),
  )
  return asUtc - Math.floor(ts / 1000) * 1000
}

/** "2026-03-01T09:30" in `timeZone` → ISO instant. */
export function zonedLocalToIso(local: string, timeZone: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})/.exec(local)
  if (!m) return ''
  const guess = Date.UTC(+m[1]!, +m[2]! - 1, +m[3]!, +m[4]!, +m[5]!)
  let ts = guess - offsetMs(guess, timeZone)
  const second = offsetMs(ts, timeZone)
  ts = guess - second
  return new Date(ts).toISOString()
}

/** ISO instant → "YYYY-MM-DDTHH:mm" wall-clock in `timeZone`. */
export function isoToZonedLocal(iso: string, timeZone: string): string {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return ''
  const parts = new Intl.DateTimeFormat('en-CA', {
    timeZone,
    hourCycle: 'h23',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).formatToParts(date)
  const get = (type: string) => parts.find((p) => p.type === type)?.value ?? ''
  return `${get('year')}-${get('month')}-${get('day')}T${get('hour')}:${get('minute')}`
}
