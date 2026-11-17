/**
 * Registers /sw.js, which caches content-hashed /assets/ files cache-first and answers page navigations with the
 * cached app shell while it revalidates it (see public/sw.js). Production only; set
 * localStorage['flow:disable-sw'] = '1' (or build with VITE_DISABLE_SW=1) to opt out, which also removes a worker
 * and caches that an earlier visit installed.
 */

const DISABLE_KEY = 'flow:disable-sw'
// The worker's caches: hashed files and the app shell (any version).
const CACHE_PATTERN = /^flow-(?:assets|shell)-/

export function serviceWorkerEnabled(
  env: { PROD?: boolean; VITE_DISABLE_SW?: string } = import.meta.env,
  nav: { serviceWorker?: unknown } = navigator,
) {
  if (!env.PROD || env.VITE_DISABLE_SW === '1' || !nav.serviceWorker) return false
  try {
    return localStorage.getItem(DISABLE_KEY) !== '1'
  } catch {
    return true
  }
}

async function removeServiceWorker() {
  const registrations = await navigator.serviceWorker.getRegistrations()
  await Promise.all(registrations.map(registration => registration.unregister()))
  if (typeof caches === 'undefined') return
  const keys = await caches.keys()
  await Promise.all(keys.filter(key => CACHE_PATTERN.test(key)).map(key => caches.delete(key)))
}

export function registerServiceWorker() {
  if (typeof navigator === 'undefined' || !('serviceWorker' in navigator)) return
  const enabled = serviceWorkerEnabled()
  // Registration waits for load so the worker never competes with the first paint.
  const run = () => {
    const task = enabled ? navigator.serviceWorker.register('/sw.js') : removeServiceWorker()
    void Promise.resolve(task).catch(() => {})
  }
  if (!enabled && !import.meta.env.PROD) return
  if (document.readyState === 'complete') run()
  else window.addEventListener('load', run, { once: true })
}
