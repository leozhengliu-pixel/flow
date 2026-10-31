import { ArrowUpRight } from 'lucide-react'
import { useI18n } from '@/i18n/i18n'
import { useDesktopSettings } from '@/hooks/use-desktop-settings'

export function SettingsDocsLink({ file, label = 'Docs' }: { file: string; label?: string }) {
  const { t } = useI18n()
  const desktop = useDesktopSettings()
  if (!desktop) return null
  return <a className="settings-desktop-docs" href={`https://github.com/leozhengliu-pixel/flow/blob/main/docs/${file}`} target="_blank" rel="noreferrer">{t(label)}<ArrowUpRight size={11} aria-hidden="true"/></a>
}
