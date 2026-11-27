import { useMemo } from 'react'
import { labelDisplayColor, labelHeaderTint } from '@/lib/label-colors'
import { useResolvedTheme } from './use-resolved-theme'

/** Label colours for the current theme: a dot's displayed colour and a label group header's tint. */
export function useLabelColors() {
  const theme = useResolvedTheme()
  return useMemo(() => ({
    dot: (color: string) => labelDisplayColor(color, theme),
    tint: (color: string, surface: 'header' | 'page' | 'row' = 'header') => labelHeaderTint(color, theme, surface),
  }), [theme])
}
