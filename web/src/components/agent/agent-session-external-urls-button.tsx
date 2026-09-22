import { useMemo, useState } from 'react'
import { ExternalLink } from 'lucide-react'
import { useI18n } from '@/i18n/i18n'
import {
  filteredExternalUrls,
  type AgentExternalUrl,
} from './agent-session-external-urls'
import styles from './agent-session-external-urls-button.module.css'

export function shouldShowAgentSessionExternalUrlsButton({
  urls,
  isPureTouchDevice = false,
}: {
  urls: AgentExternalUrl[]
  isPureTouchDevice?: boolean
}) {
  if (isPureTouchDevice) return false
  return urls.length > 0
}

/** LS-0039 AgentSessionExternalUrlsButton — single link or multi-url Links menu. */
export function AgentSessionExternalUrlsButton({
  urls: urlsProp,
  candidates,
  isPureTouchDevice = false,
  className,
}: {
  urls?: AgentExternalUrl[]
  candidates?: Array<{ url?: string | null; label?: string | null } | string | null | undefined>
  isPureTouchDevice?: boolean
  className?: string
}) {
  const { t } = useI18n()
  const [menuOpen, setMenuOpen] = useState(false)
  const urls = useMemo(
    () => urlsProp ?? filteredExternalUrls(candidates ?? []),
    [urlsProp, candidates],
  )

  if (
    !shouldShowAgentSessionExternalUrlsButton({
      urls,
      isPureTouchDevice,
    })
  ) {
    return null
  }

  const first = urls[0]!
  const multi = urls.length > 1

  if (!multi) {
    return (
      <a
        className={[styles.link, className].filter(Boolean).join(' ')}
        href={first.url}
        rel="noreferrer"
        target="_blank"
      >
        <span>{first.label}</span>
        <ExternalLink aria-hidden size={14} />
      </a>
    )
  }

  return (
    <div className={[styles.wrap, className].filter(Boolean).join(' ')}>
      <button
        aria-expanded={menuOpen}
        aria-haspopup="menu"
        className={styles.menuButton}
        onClick={() => setMenuOpen(open => !open)}
        type="button"
      >
        {t('Links')}
        <span aria-hidden className={styles.caret} />
      </button>
      {menuOpen && (
        <ul className={styles.menu} role="menu">
          {urls.map(item => (
            <li key={item.url} role="none">
              <a
                href={item.url}
                onClick={() => setMenuOpen(false)}
                rel="noreferrer"
                role="menuitem"
                target="_blank"
              >
                <span>{item.label}</span>
                <ExternalLink aria-hidden size={12} />
              </a>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
