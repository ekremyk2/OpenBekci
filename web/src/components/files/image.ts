export const MAX_IMAGE_EDGE = 2000

/**
 * Downscales an image so its longer edge is at most `maxEdge` px (canvas). Smaller images, other
 * file types and environments without canvas support are returned untouched; the server strips
 * location data from whatever arrives.
 */
export async function downscaleImage(file: File, maxEdge = MAX_IMAGE_EDGE): Promise<Blob> {
  if (!file.type.startsWith('image/') || typeof createImageBitmap !== 'function') return file
  let bitmap: ImageBitmap
  try {
    bitmap = await createImageBitmap(file)
  } catch {
    return file // e.g. HEIC the browser cannot decode: let the server handle it
  }
  try {
    const longest = Math.max(bitmap.width, bitmap.height)
    if (longest <= maxEdge) return file
    const scale = maxEdge / longest
    const width = Math.round(bitmap.width * scale)
    const height = Math.round(bitmap.height * scale)
    const canvas = document.createElement('canvas')
    canvas.width = width
    canvas.height = height
    const ctx = canvas.getContext('2d')
    if (!ctx) return file
    ctx.drawImage(bitmap, 0, 0, width, height)
    const type = file.type === 'image/png' ? 'image/png' : 'image/jpeg'
    const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, type, 0.85))
    return blob && blob.size < file.size ? blob : file
  } finally {
    bitmap.close()
  }
}
