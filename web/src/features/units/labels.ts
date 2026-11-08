import type { MessageDescriptor } from '@lingui/core'
import { msg } from '@lingui/core/macro'

const TYPE: Record<string, MessageDescriptor> = {
  flat: msg`Flat`,
  shop: msg`Shop`,
  office: msg`Office`,
  parking: msg`Parking`,
  storage: msg`Storage`,
  common: msg`Common area`,
  other: msg`Other`,
}

type Translate = { _: (d: MessageDescriptor) => string }

/** Open enum: an unknown type renders as its raw id. */
export function unitTypeLabel(type: string, i18n: Translate): string {
  const d = TYPE[type]
  return d ? i18n._(d) : type
}

/** Format a plate as the gate sees it: letters and digits grouped by kind, "34ABC123" → "34 ABC 123". */
export function formatPlate(normalized: string): string {
  return normalized.replace(/(\d+)(?=[A-Z])|([A-Z]+)(?=\d)/g, '$1$2 ').trim()
}
