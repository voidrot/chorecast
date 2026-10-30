import createClient from 'openapi-fetch'
import type { paths } from './schema'

// Types are generated from api/openapi.json via `mise run gen:web-client`.
// The web admin authenticates with an HttpOnly session cookie, so no token handling here.
export const api = createClient<paths>({ baseUrl: '/api/v1', credentials: 'same-origin' })
