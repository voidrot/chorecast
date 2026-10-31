import type { components } from './schema'

export type Problem = components['schemas']['Problem']

/**
 * An API error decoded from an RFC 9457 problem response. Branch on `code` (stable), never on
 * `detail` text. Network failures use status 0 and code "network_error".
 */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly detail: string
  readonly fieldErrors: Problem['errors']

  constructor(status: number, code: string, detail: string, fieldErrors?: Problem['errors']) {
    super(detail || code)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.detail = detail
    this.fieldErrors = fieldErrors ?? []
  }

  static fromProblem(status: number, problem: Partial<Problem> | undefined): ApiError {
    return new ApiError(
      status,
      problem?.code ?? 'error',
      problem?.detail ?? problem?.title ?? '',
      problem?.errors,
    )
  }

  static network(cause: unknown): ApiError {
    const err = new ApiError(
      0,
      'network_error',
      cause instanceof Error ? cause.message : String(cause),
    )
    err.cause = cause
    return err
  }
}

/** Parses an error response body as a problem document; tolerates non-JSON bodies. */
export async function problemFrom(response: Response): Promise<ApiError> {
  let body: Partial<Problem> | undefined
  try {
    body = (await response.clone().json()) as Partial<Problem>
  } catch {
    body = undefined
  }
  return ApiError.fromProblem(response.status, body)
}
