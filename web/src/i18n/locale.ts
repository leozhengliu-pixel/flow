import { isChineseDictionaryLoaded, loadChineseDictionary } from './translate'

export type AppLocale = 'en-US' | 'zh-CN'

export const LOCALE_STORAGE_KEY = 'flow:locale'

export function initialLocale(): AppLocale {
  const stored = localStorage.getItem(LOCALE_STORAGE_KEY)
  if (stored === 'zh-CN' || stored === 'en-US') return stored
  return navigator.language.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en-US'
}

/**
 * Resolves once the first render can show the right language. English needs no extra data; the Chinese
 * dictionary is a separate lazily loaded chunk, so it is awaited here before the app mounts to avoid a flash of English.
 */
export async function prepareInitialLocale(): Promise<AppLocale> {
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
