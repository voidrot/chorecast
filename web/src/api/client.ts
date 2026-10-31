import createClient, { type Middleware } from 'openapi-fetch'
import { problemFrom } from './errors'
import type { paths } from './schema'

// Types are generated from api/openapi.json via `mise run gen:web-client`.
// The web admin authenticates with an HttpOnly session cookie, so no token handling here.

/** App-level reactions to API state, wired in main.ts (avoids importing the router here). */
export const apiEvents = {
  onUnauthorized: () => {},
  onSetupRequired: () => {},
}

const reactions: Middleware = {
  async onResponse({ response }) {
    if (response.status === 401) {
      apiEvents.onUnauthorized()
    } else if (response.status === 503 && (await problemFrom(response)).code === 'setup_required') {
      apiEvents.onSetupRequired()
    }
    return response
  },
}

export const api = createClient<paths>({
  // Absolute, same-origin: identical in the browser, and works where Request needs absolute URLs.
  baseUrl: new URL('/api/v1', globalThis.location?.origin ?? 'http://localhost').href,
  credentials: 'same-origin',
  // Resolve fetch at call time so tests can stub it.
  fetch: (request) => globalThis.fetch(request),
})
api.use(reactions)
