/** Seconds until an ISO timestamp, never negative. */
export function secondsLeft(expiresAt: string, now: number): number {
  return Math.max(0, Math.ceil((Date.parse(expiresAt) - now) / 1000))
}

/** True when a phone on the network couldn't reach this server URL (localhost and loopback). */
export function isLocalServer(url: string): boolean {
  try {
    const host = new URL(url).hostname
    return host === 'localhost' || host === '::1' || host === '[::1]' || host.startsWith('127.')
  } catch {
    return false
  }
}
