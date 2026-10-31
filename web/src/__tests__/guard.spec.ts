import { describe, expect, it } from 'vitest'
import { decide, type Target } from '@/router/guard'
import type { SessionStatus } from '@/stores/session'

const route = (name: string, fullPath: string, isPublic = false): Target => ({
  name,
  fullPath,
  meta: { public: isPublic },
})
const home = route('home', '/')
const deep = route('kids', '/kids/sam?tab=points')
const login = route('login', '/login', true)
const setup = route('setup', '/setup', true)
const notFound = route('not-found', '/nope', true)

describe('router guard', () => {
  const cases: [SessionStatus, Target, ReturnType<typeof decide>][] = [
    ['authenticated', home, true],
    ['authenticated', login, { name: 'home' }],
    ['authenticated', setup, { name: 'home' }],
    ['anonymous', home, { name: 'login', query: {} }],
    ['anonymous', deep, { name: 'login', query: { redirect: '/kids/sam?tab=points' } }],
    ['anonymous', login, true],
    ['anonymous', setup, { name: 'login' }],
    ['anonymous', notFound, true],
    ['forbidden', home, { name: 'login', query: {} }],
    ['setup_required', home, { name: 'setup' }],
    ['setup_required', login, { name: 'setup' }],
    ['setup_required', setup, true],
    ['unknown', deep, { name: 'login', query: { redirect: '/kids/sam?tab=points' } }],
    ['unknown', login, true],
  ]
  it.each(cases)('%s → %s', (status, to, want) => {
    expect(decide(status, to)).toEqual(want)
  })
})
