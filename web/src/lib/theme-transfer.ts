/**
 * AccountPreferences theme import/copy helpers (settings P1 LS-0005).
 * Transfers interface + code theme prefs as JSON — no billing fields.
 */
import type { ThemeSettings } from '@/lib/theme'
import { applyTheme, readThemeSettings } from '@/lib/theme'

export interface ThemeTransferPayload {
  version: 1
  exportedAt: string
  interfaceTheme?: string
  lightTheme?: string
  darkTheme?: string
  codeTheme?: string
  codeFont?: string
  fontSize?: string
}

export type ThemeTransferAccountPrefs = {
  interfaceTheme?: string
  lightTheme?: string
  darkTheme?: string
  codeTheme?: string
  codeFont?: string
  fontSize?: string
}

export function buildThemeTransferPayload(
  prefs: ThemeTransferAccountPrefs,
  theme: ThemeSettings = readThemeSettings(),
): ThemeTransferPayload {
  return {
    version: 1,
    exportedAt: new Date().toISOString(),
    interfaceTheme: prefs.interfaceTheme ?? theme.interfaceTheme,
    lightTheme: prefs.lightTheme ?? theme.lightTheme,
    darkTheme: prefs.darkTheme ?? theme.darkTheme,
    codeTheme: prefs.codeTheme,
    codeFont: prefs.codeFont,
    fontSize: prefs.fontSize,
  }
}

export function serializeThemeTransfer(payload: ThemeTransferPayload): string {
  return `${JSON.stringify(payload, null, 2)}\n`
}

export function parseThemeTransfer(raw: string): ThemeTransferPayload {
  const parsed = JSON.parse(raw) as ThemeTransferPayload
  if (!parsed || parsed.version !== 1) {
    throw new Error('Unsupported theme transfer payload')
  }
  return parsed
}

export function applyThemeTransfer(
  payload: ThemeTransferPayload,
): ThemeTransferAccountPrefs {
  const next: ThemeTransferAccountPrefs = {}
  if (payload.interfaceTheme) next.interfaceTheme = payload.interfaceTheme
  if (payload.lightTheme) next.lightTheme = payload.lightTheme
  if (payload.darkTheme) next.darkTheme = payload.darkTheme
  if (payload.codeTheme) next.codeTheme = payload.codeTheme
  if (payload.codeFont) next.codeFont = payload.codeFont
  if (payload.fontSize) next.fontSize = payload.fontSize
  applyTheme({
    interfaceTheme: next.interfaceTheme,
    lightTheme: next.lightTheme,
    darkTheme: next.darkTheme,
  })
  return next
}
