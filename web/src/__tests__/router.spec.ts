import { describe, expect, it } from 'vitest'
import router from '@/router'

describe('router', () => {
  it('has a home route', () => {
    expect(router.hasRoute('home')).toBe(true)
  })
})
