import { describe, expect, it } from 'vitest'
import { isLocalServer, secondsLeft } from '@/devices/pairing'

describe('pairing helpers', () => {
  it('counts down to expiry', () => {
    const now = Date.parse('2026-10-31T12:00:00Z')
    expect(secondsLeft('2026-10-31T12:05:00Z', now)).toBe(300)
    expect(secondsLeft('2026-10-31T12:00:00.5Z', now)).toBe(1)
    expect(secondsLeft('2026-10-31T11:59:00Z', now)).toBe(0)
  })
  it('spots servers a phone cannot reach', () => {
    expect(isLocalServer('http://localhost:8080')).toBe(true)
    expect(isLocalServer('http://127.0.0.1:8080')).toBe(true)
    expect(isLocalServer('http://192.168.1.20:8080')).toBe(false)
    expect(isLocalServer('https://chores.example.com')).toBe(false)
  })
})
