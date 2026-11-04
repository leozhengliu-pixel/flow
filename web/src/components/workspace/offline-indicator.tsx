import { useOptionalTranslate } from '@/i18n/i18n'
import './offline-indicator.css'

// A 5x5 dot matrix; the brighter dots draw the shape and the rest stay dim.
const DOTS = [
  '..#..',
  '.##..',
  '#####',
  '..##.',
  '..#..',
]

/** Shown in the sidebar header in place of the workspace name while the browser is offline. */
export function OfflineIndicator() {
  const t = useOptionalTranslate()
  return (
    <span className="workspace-offline" role="status">
      <svg className="workspace-offline__icon" viewBox="0 0 16 16" aria-hidden="true">
        {DOTS.flatMap((row, y) => [...row].map((cell, x) => (
          <circle key={`${x}-${y}`} cx={1 + x * 3.5} cy={1 + y * 3.5} r="1" className={cell === '#' ? 'is-bright' : undefined} />
        )))}
      </svg>
      <span>{t('Offline')}</span>
    </span>
  )
}
