import { onBeforeUnmount, onMounted, type Ref, watch } from 'vue'

/** Signed links stop working at their expiry; refetch this long before it. */
const MARGIN_MS = 30_000

/** The earliest of the given expiry timestamps, as epoch ms, or undefined when there are none. */
export function earliestExpiry(expiries: Iterable<string | undefined>): number | undefined {
  let min: number | undefined
  for (const e of expiries) {
    const t = e ? Date.parse(e) : Number.NaN
    if (!Number.isNaN(t) && (min === undefined || t < min)) min = t
  }
  return min
}

/** Milliseconds until links expiring at `expiresAt` (epoch ms) should be refetched, at least 0. */
export function refreshDelay(expiresAt: number, now: number): number {
  return Math.max(0, expiresAt - MARGIN_MS - now)
}

/**
 * Calls `refetch` shortly before the earliest of the signed links expires, and when the window
 * regains focus with a link expired or about to. Refetching changes `expiresAt`, which re-arms it.
 */
export function useLinkRefresh(expiresAt: Ref<number | undefined>, refetch: () => unknown) {
  let timer: ReturnType<typeof setTimeout> | undefined

  function arm() {
    clearTimeout(timer)
    timer = undefined
    if (expiresAt.value === undefined) return
    // setTimeout can't take more than ~24.8 days; links last minutes.
    timer = setTimeout(refetch, Math.min(refreshDelay(expiresAt.value, Date.now()), 2 ** 31 - 1))
  }

  function onFocus() {
    if (expiresAt.value !== undefined && refreshDelay(expiresAt.value, Date.now()) === 0) refetch()
  }

  watch(expiresAt, arm, { immediate: true })
  onMounted(() => window.addEventListener('focus', onFocus))
  onBeforeUnmount(() => {
    clearTimeout(timer)
    window.removeEventListener('focus', onFocus)
  })
}
