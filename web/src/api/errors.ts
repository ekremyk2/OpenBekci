/** application/problem+json → AppError (web plan §6 error table). */

export interface FieldIssue {
  code: string
  message?: string
}

interface ProblemBody {
  type?: string
  title?: string
  status?: number
  detail?: string
  code?: string
  errors?: { field?: string; code?: string; message?: string }[]
  request_id?: string
}

export class AppError extends Error {
  readonly code: string
  readonly status: number
  /** Server-side validation issues keyed by field path (`errors[].field`). */
  readonly fieldErrors: Record<string, FieldIssue[]>
  readonly requestId?: string
  /** Seconds, from `Retry-After` on 429. */
  readonly retryAfter?: number

  constructor(init: {
    status: number
    code: string
    message?: string
    fieldErrors?: Record<string, FieldIssue[]>
    requestId?: string
    retryAfter?: number
  }) {
    super(init.message ?? init.code)
    this.name = 'AppError'
    this.status = init.status
    this.code = init.code
    this.fieldErrors = init.fieldErrors ?? {}
    this.requestId = init.requestId
    this.retryAfter = init.retryAfter
  }

  get isUnauthorized() {
    return this.status === 401
  }
  get isForbidden() {
    return this.status === 403
  }
  get isReauthRequired() {
    return this.status === 403 && this.code === 'reauth_required'
  }
  get isVersionConflict() {
    return this.status === 412 || this.code === 'version_conflict'
  }
  get isValidation() {
    return this.status === 422
  }
  get isRateLimited() {
    return this.status === 429
  }
}

const FALLBACK_CODES: Record<number, string> = {
  400: 'validation_failed',
  401: 'invalid_grant',
  403: 'insufficient_permission',
  404: 'not_found',
  409: 'conflict',
  412: 'version_conflict',
  422: 'validation_failed',
  429: 'rate_limited',
}

function parseRetryAfter(value: string | null): number | undefined {
  if (!value) return undefined
  const seconds = Number(value)
  if (Number.isFinite(seconds)) return Math.max(0, seconds)
  const date = Date.parse(value)
  return Number.isNaN(date) ? undefined : Math.max(0, Math.ceil((date - Date.now()) / 1000))
}

/** Builds an AppError from an HTTP response and its (possibly non-JSON) body. */
export function appErrorFromResponse(response: Response, body: unknown): AppError {
  const problem: ProblemBody = body && typeof body === 'object' ? (body as ProblemBody) : {}
  const status = response.status
  const fieldErrors: Record<string, FieldIssue[]> = {}
  for (const e of problem.errors ?? []) {
    if (!e.field) continue
    ;(fieldErrors[e.field] ??= []).push({ code: e.code ?? 'invalid', message: e.message })
  }
  return new AppError({
    status,
    code: problem.code ?? FALLBACK_CODES[status] ?? (status >= 500 ? 'internal_error' : 'unknown'),
    message: problem.detail ?? problem.title,
    fieldErrors,
    requestId: problem.request_id,
    retryAfter: status === 429 ? parseRetryAfter(response.headers.get('Retry-After')) : undefined,
  })
}

export function isAppError(e: unknown): e is AppError {
  return e instanceof AppError
}
