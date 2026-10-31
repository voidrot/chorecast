import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, apiEvents } from '@/api/client'
import { ApiError, problemFrom } from '@/api/errors'
import { useSessionStore } from '@/stores/session'

function respond(status: number, body: unknown, contentType = 'application/json') {
  return new Response(typeof body === 'string' ? body : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': contentType },
  })
}

const problem = (status: number, code: string, detail = '') =>
  respond(status, { status, code, detail, title: 'x' }, 'application/problem+json')

let fetchMock: ReturnType<typeof vi.fn>

beforeEach(() => {
  setActivePinia(createPinia())
  fetchMock = vi.fn()
  vi.stubGlobal('fetch', fetchMock)
})
afterEach(() => {
  vi.unstubAllGlobals()
  apiEvents.onUnauthorized = () => {}
  apiEvents.onSetupRequired = () => {}
})

describe('problemFrom', () => {
  it('decodes problem documents', async () => {
    const err = await problemFrom(problem(404, 'kid_not_found', 'no kid matches "sam"'))
    expect(err).toBeInstanceOf(ApiError)
    expect(err).toMatchObject({
      status: 404,
      code: 'kid_not_found',
      detail: 'no kid matches "sam"',
    })
  })
  it('tolerates non-JSON bodies', async () => {
    const err = await problemFrom(respond(502, '<html>bad gateway</html>', 'text/html'))
    expect(err).toMatchObject({ status: 502, code: 'error' })
  })
})

describe('api client', () => {
  it('requests the API under the page origin', async () => {
    fetchMock.mockResolvedValue(respond(200, { status: 'ok' }))
    await api.GET('/health')
    const req = fetchMock.mock.calls[0][0] as Request
    expect(new URL(req.url).pathname).toBe('/api/v1/health')
  })
  it('signals 401 and setup_required', async () => {
    const unauthorized = vi.fn()
    const setupRequired = vi.fn()
    apiEvents.onUnauthorized = unauthorized
    apiEvents.onSetupRequired = setupRequired

    fetchMock.mockResolvedValueOnce(problem(401, 'unauthenticated'))
    await api.GET('/me')
    fetchMock.mockResolvedValueOnce(problem(503, 'setup_required'))
    await api.GET('/me')
    fetchMock.mockResolvedValueOnce(problem(503, 'unavailable'))
    await api.GET('/me')

    expect(unauthorized).toHaveBeenCalledTimes(1)
    expect(setupRequired).toHaveBeenCalledTimes(1)
  })
})

describe('session store', () => {
  it.each([
    [
      respond(200, { kind: 'parent', owner: true, userId: 1, scopes: ['server:manage'] }),
      'authenticated',
    ],
    [respond(200, { kind: 'kid', owner: false, userId: 2, scopes: [] }), 'forbidden'],
    [problem(401, 'unauthenticated'), 'anonymous'],
    [problem(503, 'setup_required'), 'setup_required'],
    [problem(500, 'internal_error', 'boom'), 'unknown'],
  ])('maps /me response to %#', async (response, want) => {
    fetchMock.mockResolvedValue(response)
    const session = useSessionStore()
    expect(await session.load()).toBe(want)
  })

  it('knows owners and reports network errors', async () => {
    const session = useSessionStore()
    fetchMock.mockResolvedValue(
      respond(200, { kind: 'parent', owner: true, userId: 1, scopes: [] }),
    )
    await session.load()
    expect(session.isOwner).toBe(true)

    fetchMock.mockRejectedValue(new TypeError('Failed to fetch'))
    expect(await session.load()).toBe('unknown')
    expect(session.error).toMatchObject({ code: 'network_error', detail: 'Failed to fetch' })
  })
})
