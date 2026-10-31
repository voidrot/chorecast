import { type Ref, ref, shallowRef } from 'vue'
import { ApiError } from '@/api/errors'

/**
 * Tracks loading and error state for an async action. Errors are normalized to ApiError so views
 * can show `error.detail` and branch on `error.code`.
 */
export function useAsync<T, A extends unknown[]>(fn: (...args: A) => Promise<T>) {
  const data = shallowRef<T>()
  const error: Ref<ApiError | undefined> = ref()
  const loading = ref(false)

  async function run(...args: A): Promise<T | undefined> {
    loading.value = true
    error.value = undefined
    try {
      data.value = await fn(...args)
      return data.value
    } catch (e) {
      error.value = e instanceof ApiError ? e : ApiError.network(e)
      return undefined
    } finally {
      loading.value = false
    }
  }

  return { data, error, loading, run }
}
