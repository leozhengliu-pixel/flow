/* oxlint-disable react/only-export-components -- locale hooks and components share one provider contract. */
import { createContext, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useState, type ReactNode } from 'react'

import { SelectControl } from '@/components/ui/select-control'

import { initialLocale, LOCALE_STORAGE_KEY, type AppLocale } from './locale'
import { isChineseDictionaryLoaded, loadChineseDictionary, translateToChinese } from './translate'
import { formatRelativeTime } from '@/lib/relative-time'

export type { AppLocale }

type I18nValue = {
  locale: AppLocale
  setLocale: (locale: AppLocale) => void
  t: (source: string) => string
  formatDate: (value: Date | string | number, options?: Intl.DateTimeFormatOptions) => string
  formatNumber: (value: number, options?: Intl.NumberFormatOptions) => string
  /** Compact relative time ("31min ago" / "31分钟前") in the active locale. */
  formatRelative: (value: Date | string | number) => string
}

const I18nContext = createContext<I18nValue | null>(null)

export function I18nProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<AppLocale>(initialLocale)
  const [dictionaryReady, setDictionaryReady] = useState(isChineseDictionaryLoaded)
  const setLocale = useCallback((next: AppLocale) => {
    const apply = () => {
      localStorage.setItem(LOCALE_STORAGE_KEY, next)
      setLocaleState(next)
    }
    // Switch only once the Chinese dictionary has arrived so the UI never shows a half-translated state.
    if (next !== 'zh-CN' || isChineseDictionaryLoaded()) apply()
    else void loadChineseDictionary().then(() => { setDictionaryReady(true); apply() }, () => undefined)
  }, [])
  const t = useCallback((source: string) => locale === 'zh-CN' && dictionaryReady ? translateToChinese(source) : source, [locale, dictionaryReady])
  const value = useMemo<I18nValue>(() => ({
    locale,
    setLocale,
    t,
    formatDate: (input, options) => new Intl.DateTimeFormat(locale, options).format(new Date(input)),
    formatNumber: (input, options) => new Intl.NumberFormat(locale, options).format(input),
    formatRelative: input => formatRelativeTime(input, locale),
  }), [locale, setLocale, t])

  useEffect(() => {
    if (locale !== 'zh-CN' || dictionaryReady) return
    let cancelled = false
    void loadChineseDictionary().then(() => { if (!cancelled) setDictionaryReady(true) }, () => undefined)
    return () => { cancelled = true }
  }, [locale, dictionaryReady])

  useEffect(() => {
    document.documentElement.lang = locale === 'zh-CN' ? 'zh-CN' : 'en'
    document.documentElement.dataset.locale = locale
  }, [locale])

  return <I18nContext.Provider value={value}>{children}<LegacyUiTranslator locale={locale} dictionaryReady={dictionaryReady}/></I18nContext.Provider>
}

export function useI18n() {
  const value = useContext(I18nContext)
  if (!value) throw new Error('useI18n must be used inside I18nProvider')
  return value
}

export function LanguageSelect({ className }: { className?: string }) {
  const { locale, setLocale, t } = useI18n()
  return <label className={className} data-i18n-control data-i18n-ignore>
    <span>{t('Language')}</span>
    <SelectControl
      className="language-select-control"
      label={t('Language')}
      onChange={next => setLocale(next as AppLocale)}
      options={[
        { value: 'en-US', label: 'English' },
        { value: 'zh-CN', label: '简体中文' },
      ]}
      value={locale}
    />
  </label>
}

type TextState = { source: string; rendered: string }
const textStates = new WeakMap<Text, TextState>()
const attributeStates = new WeakMap<Element, Map<string, TextState>>()
const attributes = ['aria-label', 'placeholder', 'title', 'data-placeholder'] as const

function LegacyUiTranslator({ locale, dictionaryReady }: { locale: AppLocale; dictionaryReady: boolean }) {
  useLayoutEffect(() => {
    const translateTree = (root: ParentNode) => {
      const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT)
      let node: Node | null
      while ((node = walker.nextNode())) translateTextNode(node as Text, locale)
      if (root instanceof Element) translateAttributes(root, locale)
      root.querySelectorAll?.('*').forEach(element => translateAttributes(element, locale))
    }
    translateTree(document.body)
    const observer = new MutationObserver(records => {
      for (const record of records) {
        if (record.type === 'characterData') translateTextNode(record.target as Text, locale)
        if (record.type === 'attributes') translateAttributes(record.target as Element, locale)
        record.addedNodes.forEach(node => {
          if (node.nodeType === Node.TEXT_NODE) translateTextNode(node as Text, locale)
          else if (node instanceof Element) translateTree(node)
        })
      }
    })
    observer.observe(document.body, { subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: [...attributes] })
    return () => observer.disconnect()
  }, [locale, dictionaryReady])
  return null
}

function translateTextNode(node: Text, locale: AppLocale) {
  if (shouldIgnore(node.parentElement)) return
  const current = node.data
  const previous = textStates.get(node)
  const source = previous && (current === previous.source || current === previous.rendered) ? previous.source : current
  const rendered = locale === 'zh-CN' ? translateToChinese(source) : source
  textStates.set(node, { source, rendered })
  if (current !== rendered) node.data = rendered
}

function translateAttributes(element: Element, locale: AppLocale) {
  if (shouldIgnore(element)) return
  let states = attributeStates.get(element)
  if (!states) {
    states = new Map()
    attributeStates.set(element, states)
  }
  for (const attribute of attributes) {
    const current = element.getAttribute(attribute)
    if (current == null) continue
    const previous = states.get(attribute)
    const source = previous && (current === previous.source || current === previous.rendered) ? previous.source : current
    const rendered = locale === 'zh-CN' ? translateToChinese(source) : source
    states.set(attribute, { source, rendered })
    if (current !== rendered) element.setAttribute(attribute, rendered)
  }
}

function shouldIgnore(element: Element | null) {
  if (!element) return true
  return Boolean(element.closest('[data-i18n-ignore], [contenteditable="true"], .ProseMirror, .tiptap, script, style, code'))
}
