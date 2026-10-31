import { shallowRef } from 'vue'
import { api } from '@/api/client'
import type { components } from '@/api/schema'

export type ServerInfo = components['schemas']['VersionOutputBody']

const info = shallowRef<ServerInfo>()
let pending: Promise<void> | undefined

/** The server's version and features (e.g. whether SSO is configured), loaded once per page load. */
export function useServerInfo() {
  if (!info.value && !pending) {
    pending = api
      .GET('/version')
      .then(({ data }) => {
        info.value = data
      })
      .catch(() => {})
      .finally(() => {
        pending = undefined
      })
  }
  return info
}
