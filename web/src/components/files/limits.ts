/*
 * Client-side upload checks. They mirror the server's limits (backend §13: sniffed mime, size
 * ceiling) so people get an answer before the bytes leave; the server still decides. The draft
 * contract does not publish the numbers (see PR notes), so they live here in one table.
 */

export type UploadProfile = 'photo' | 'document' | 'any'

export interface UploadLimits {
  /** Exact mime types, or `type/*` wildcards. */
  mime: readonly string[]
  /** File extensions (lower case, with dot) accepted when the browser reports no mime type. */
  extensions: readonly string[]
  maxBytes: number
}

const IMAGE_MIME = ['image/jpeg', 'image/png', 'image/webp', 'image/heic', 'image/heif']
const IMAGE_EXT = ['.jpg', '.jpeg', '.png', '.webp', '.heic', '.heif']
const MB = 1024 * 1024

export const UPLOAD_LIMITS: Record<UploadProfile, UploadLimits> = {
  // The backend accepts 25 MiB for every file. Photos are downscaled before upload, but the
  // original is only checked against the server's ceiling.
  photo: { mime: IMAGE_MIME, extensions: IMAGE_EXT, maxBytes: 25 * MB },
  document: {
    mime: [
      ...IMAGE_MIME,
      'application/pdf',
      'text/plain',
      'text/csv',
      'application/msword',
      'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
      'application/vnd.ms-excel',
      'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
      'application/vnd.oasis.opendocument.text',
      'application/vnd.oasis.opendocument.spreadsheet',
    ],
    extensions: [
      ...IMAGE_EXT,
      '.pdf',
      '.txt',
      '.csv',
      '.doc',
      '.docx',
      '.xls',
      '.xlsx',
      '.odt',
      '.ods',
    ],
    maxBytes: 25 * MB,
  },
  any: { mime: ['*/*'], extensions: [], maxBytes: 25 * MB },
}

export type UploadRejection = 'empty' | 'too_large' | 'type_not_allowed' | 'too_many'

export function extensionOf(name: string): string {
  const i = name.lastIndexOf('.')
  return i < 0 ? '' : name.slice(i).toLowerCase()
}

export function isImageMime(mime: string): boolean {
  return mime.startsWith('image/')
}

/** `null` when the file may be uploaded. */
export function checkFile(
  file: { name: string; size: number; type: string },
  profile: UploadProfile,
): UploadRejection | null {
  const limits = UPLOAD_LIMITS[profile]
  if (file.size === 0) return 'empty'
  if (file.size > limits.maxBytes) return 'too_large'
  if (limits.mime.includes('*/*')) return null
  const type = file.type.toLowerCase()
  if (type) {
    const ok = limits.mime.some((m) =>
      m.endsWith('/*') ? type.startsWith(m.slice(0, -1)) : m === type,
    )
    return ok ? null : 'type_not_allowed'
  }
  return limits.extensions.includes(extensionOf(file.name)) ? null : 'type_not_allowed'
}

/** `accept` attribute for the file input. */
export function acceptFor(profile: UploadProfile): string | undefined {
  const l = UPLOAD_LIMITS[profile]
  if (l.mime.includes('*/*')) return undefined
  return [...l.mime, ...l.extensions].join(',')
}
