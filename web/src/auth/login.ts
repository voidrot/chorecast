import type { ApiError } from '@/api/errors'

/** Which step of the login form to show. */
export type LoginStep = 'password' | 'totp' | 'recovery'

/** What a failed login means for the form: the message to show and the step to move to. */
export interface LoginFailure {
  messageKey: string
  params?: Record<string, number>
  step?: LoginStep
}

export function loginFailure(
  err: ApiError,
  retryAfter: string | null,
  step: LoginStep,
): LoginFailure {
  switch (err.code) {
    case 'invalid_credentials':
      return { messageKey: 'login.errors.invalidCredentials', step: 'password' }
    case 'totp_required':
      return { messageKey: '', step: step === 'password' ? 'totp' : step }
    case 'invalid_totp_code':
      return {
        messageKey:
          step === 'recovery' ? 'login.errors.invalidRecovery' : 'login.errors.invalidCode',
      }
    case 'rate_limited': {
      const secs = Number(retryAfter)
      const minutes = Number.isFinite(secs) && secs > 0 ? Math.ceil(secs / 60) : 15
      return { messageKey: 'login.errors.rateLimited', params: { minutes } }
    }
    case 'network_error':
      return { messageKey: 'login.errors.network' }
    default:
      return { messageKey: 'login.errors.unexpected' }
  }
}

/** Only follow same-app redirects (a path), never another origin. */
export function safeRedirect(value: unknown): string {
  return typeof value === 'string' && value.startsWith('/') && !value.startsWith('//') ? value : '/'
}

/** i18n key for an SSO error code passed back on redirect (?sso_error=…). */
export function ssoErrorKey(code: unknown): string | undefined {
  switch (code) {
    case undefined:
    case null:
    case '':
      return undefined
    case 'not_linked':
    case 'already_linked':
    case 'expired':
    case 'denied':
      return `sso.errors.${code}`
    default:
      return 'sso.errors.failed'
  }
}

/** The URL that starts SSO sign-in in the browser, returning to `redirect` afterwards. */
export function ssoStartURL(redirect: string): string {
  const q = new URLSearchParams({ client: 'web' })
  if (redirect !== '/') q.set('redirect', redirect)
  return `/api/v1/auth/oidc/start?${q}`
}
