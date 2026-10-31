import type { RouteLocationRaw, RouteMeta } from 'vue-router'
import type { SessionStatus } from '@/stores/session'

declare module 'vue-router' {
  interface RouteMeta {
    /** Reachable without signing in (login, setup, not found). */
    public?: boolean
  }
}

export interface Target {
  name?: string | symbol | null
  fullPath: string
  meta: RouteMeta
}

/**
 * Where navigation to `to` should go for a session status: `true` to proceed, or a redirect.
 * Kept pure so the rules are unit-tested.
 */
export function decide(status: SessionStatus, to: Target): true | RouteLocationRaw {
  const name = to.name
  switch (status) {
    case 'setup_required':
      return name === 'setup' ? true : { name: 'setup' }
    case 'authenticated':
      return name === 'login' || name === 'setup' ? { name: 'home' } : true
    case 'anonymous':
    case 'forbidden':
      if (name === 'setup') return { name: 'login' }
      if (to.meta.public) return true
      return { name: 'login', query: to.fullPath === '/' ? {} : { redirect: to.fullPath } }
    case 'unknown':
      // Server unreachable: let public pages render; protected ones go to login, which shows it.
      return to.meta.public ? true : { name: 'login', query: { redirect: to.fullPath } }
  }
}
