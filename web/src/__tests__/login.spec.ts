import { describe, expect, it } from 'vitest'
import { ApiError } from '@/api/errors'
import { loginFailure, safeRedirect } from '@/auth/login'

const err = (status: number, code: string) => new ApiError(status, code, '')

describe('loginFailure', () => {
  it('moves to the code step when 2FA is required', () => {
    expect(loginFailure(err(401, 'totp_required'), null, 'password')).toEqual({
      messageKey: '',
      step: 'totp',
    })
    expect(loginFailure(err(401, 'totp_required'), null, 'recovery').step).toBe('recovery')
  })
  it('sends a wrong password back to the password step', () => {
    expect(loginFailure(err(401, 'invalid_credentials'), null, 'totp')).toEqual({
      messageKey: 'login.errors.invalidCredentials',
      step: 'password',
    })
  })
  it('tells codes and recovery codes apart', () => {
    expect(loginFailure(err(401, 'invalid_totp_code'), null, 'totp').messageKey).toBe(
      'login.errors.invalidCode',
    )
    expect(loginFailure(err(401, 'invalid_totp_code'), null, 'recovery').messageKey).toBe(
      'login.errors.invalidRecovery',
    )
  })
  it('rounds Retry-After up to minutes', () => {
    expect(loginFailure(err(429, 'rate_limited'), '61', 'password').params).toEqual({ minutes: 2 })
    expect(loginFailure(err(429, 'rate_limited'), null, 'password').params).toEqual({ minutes: 15 })
  })
})

describe('safeRedirect', () => {
  it('only allows paths in this app', () => {
    expect(safeRedirect('/account')).toBe('/account')
    expect(safeRedirect('//evil.example')).toBe('/')
    expect(safeRedirect('https://evil.example')).toBe('/')
    expect(safeRedirect(undefined)).toBe('/')
  })
})
