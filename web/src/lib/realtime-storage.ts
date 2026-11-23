import type { ChannelLike } from './shared-realtime'

export function storageChannel(name: string): ChannelLike | undefined {
  const key = `${name}:message`
  try { localStorage.getItem(key) }
  catch { return undefined }
  const receive = (event: StorageEvent) => {
    if (event.key !== key || !event.newValue) return
    try { channel.onmessage?.({ data: JSON.parse(event.newValue).message }) }
    catch { /* Ignore a malformed or obsolete message. */ }
  }
  const channel: ChannelLike = {
    onmessage: null,
    postMessage(message) {
      localStorage.setItem(key, JSON.stringify({ nonce: crypto.randomUUID(), message }))
      localStorage.removeItem(key)
    },
    close() { window.removeEventListener('storage', receive) },
  }
  window.addEventListener('storage', receive)
  return channel
}

const renewMs = 1500
const leaseMs = 10_000

/** Continually checks ownership, including when a suspended tab resumes. */
export function holdRealtimeLease(name: string, id: string, onOwner: (owned: boolean) => void) {
  const key = `${name}:lease`
  let owned = false
  let claiming: ReturnType<typeof setTimeout> | undefined
  let active = true
  const read = (): { id: string; until: number } | null => {
    try { return JSON.parse(localStorage.getItem(key) ?? 'null') }
    catch { return null }
  }
  const write = () => {
    try { localStorage.setItem(key, JSON.stringify({ id, until: Date.now() + leaseMs })); return true }
    catch { return false }
  }
  const setOwned = (next: boolean) => {
    if (owned === next) return
    owned = next
    onOwner(next)
  }
  const check = () => {
    if (!active) return
    const lease = read()
    if (owned) {
      if (lease?.id === id && lease.until > Date.now() && write()) return
      setOwned(false)
    }
    if (claiming || lease && lease.until > Date.now() && lease.id !== id) return
    if (!write()) return
    // Let competing writes settle before either tab opens the stream.
    claiming = setTimeout(() => {
      claiming = undefined
      const candidate = read()
      if (active && candidate?.id === id && candidate.until > Date.now()) setOwned(true)
    }, 150)
  }
  const storage = (event: StorageEvent) => {
    if (event.key === key || event.key === null) check()
  }
  const release = () => {
    clearTimeout(claiming)
    claiming = undefined
    setOwned(false)
    try { if (read()?.id === id) localStorage.removeItem(key) }
    catch { /* The lease expires. */ }
  }
  const hide = () => { active = false; release() }
  const show = () => { active = true; check() }
  const timer = setInterval(check, renewMs)
  window.addEventListener('storage', storage)
  window.addEventListener('pagehide', hide)
  window.addEventListener('pageshow', show)
  check()
  return () => {
    clearInterval(timer)
    window.removeEventListener('storage', storage)
    window.removeEventListener('pagehide', hide)
    window.removeEventListener('pageshow', show)
    hide()
  }
}
