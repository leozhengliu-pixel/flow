import { isChineseDictionaryLoaded, loadChineseDictionary } from './translate'

export type AppLocale = 'en-US' | 'zh-CN'

export const LOCALE_STORAGE_KEY = 'flow:locale'

/** How long the first paint waits for the server's default language. */
const DEFAULT_LANGUAGE_TIMEOUT_MS = 1500

/** The deployment's default language (defaults.user.language of the server's config file), once fetched. */
let serverDefaultLocale: AppLocale | null = null

function supportedLocale(value: unknown): AppLocale | null {
  return value === 'zh-CN' || value === 'en-US' ? value : null
}

function storedLocale(): AppLocale | null {
  return supportedLocale(localStorage.getItem(LOCALE_STORAGE_KEY))
}

/**
 * The language of the first render: the one chosen on this device, else the deployment's default language, else
 * the browser's. The account language replaces it once the workspace data arrives (useAccountLanguage).
 */
export function initialLocale(): AppLocale {
  return storedLocale() ?? serverDefaultLocale ?? (navigator.language.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en-US')
}

/**
 * Reads the deployment's default language from the public sign-in configuration when this device has none saved.
 * It never blocks the first paint for long, and a failed or slow request keeps the browser's language.
 */
async function loadServerDefaultLocale(): Promise<void> {
  if (storedLocale() || serverDefaultLocale || typeof fetch !== 'function') return
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), DEFAULT_LANGUAGE_TIMEOUT_MS)
  try {
    const response = await fetch('/api/auth/providers', { credentials: 'same-origin', signal: controller.signal })
    if (!response.ok) return
    const body = await response.json() as { defaultLanguage?: unknown }
    serverDefaultLocale = supportedLocale(body?.defaultLanguage)
  } catch {
    // Offline, aborted or not JSON: the browser's language applies.
  } finally {
    clearTimeout(timer)
  }
}

/**
 * Resolves once the first render can show the right language. English needs no extra data; the Chinese
 * dictionary is a separate lazily loaded chunk, so it is awaited here before the app mounts to avoid a flash of English.
 */
export async function prepareInitialLocale(): Promise<AppLocale> {
  await loadServerDefaultLocale()
  const locale = initialLocale()
  if (locale === 'zh-CN' && !isChineseDictionaryLoaded()) {
    try {
      await loadChineseDictionary()
    } catch {
      // I18nProvider retries the load once mounted.
    }
  }
  return locale
}
