import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/errors'
import type { components } from '@/api/schema'

export type Me = components['schemas']['MeOutputBody']

/**
 * unknown: not loaded yet (or the server is unreachable) · anonymous: not signed in ·
 * forbidden: signed in, but not as a parent · authenticated: a parent · setup_required: first-run
 * setup isn't done.
 */
export type SessionStatus =
  | 'unknown'
  | 'anonymous'
  | 'forbidden'
  | 'authenticated'
  | 'setup_required'

export const useSessionStore = defineStore('session', () => {
  const status = ref<SessionStatus>('unknown')
  const me = ref<Me>()
  const error = ref<ApiError>()

  const isOwner = computed(() => status.value === 'authenticated' && me.value?.owner === true)

  async function load(): Promise<SessionStatus> {
    error.value = undefined
    try {
      const { data, error: body, response } = await api.GET('/me')
      if (data) {
        me.value = data
        status.value = data.kind === 'parent' ? 'authenticated' : 'forbidden'
      } else {
        me.value = undefined
        // openapi-fetch has already parsed the error body.
        const problem = ApiError.fromProblem(response.status, body)
        if (response.status === 401) status.value = 'anonymous'
        else if (problem.code === 'setup_required') status.value = 'setup_required'
        else {
          status.value = 'unknown'
          error.value = problem
        }
      }
    } catch (e) {
      status.value = 'unknown'
      error.value = ApiError.network(e)
    }
    return status.value
  }

  function signedOut() {
    me.value = undefined
    status.value = 'anonymous'
  }

  return { status, me, error, isOwner, load, signedOut }
})
