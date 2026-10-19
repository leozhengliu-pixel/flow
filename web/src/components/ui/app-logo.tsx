import type { ReactNode } from 'react'

import { matchKnownApp, type KnownAppClient, type ResolvedKnownApp } from '@/lib/known-apps'
import { ApplicationIconPlaceholder } from '@/components/ui/application-icon-placeholder'

import './app-logo.css'

/** A well-known app's brand mark (an SVG sized by `size`, in the brand colour or the surrounding text colour). */
export function KnownAppMark({ app, size = 16, className, decorative = false }: { app: ResolvedKnownApp; size?: number; className?: string; decorative?: boolean }) {
  const a11y = decorative ? { 'aria-hidden': true } : { role: 'img', 'aria-label': app.name }
  return <svg className={['known-app-mark', className].filter(Boolean).join(' ')} width={size} height={size} viewBox={app.mark.viewBox} {...a11y} data-known-app={app.id} fill={app.mark.color ?? 'currentColor'}>
    <path d={app.mark.path}/>
  </svg>
}

type AppLogoProps = KnownAppClient & {
  /** The application's own image (an uploaded icon or the client's `logo_uri`); wins over a recognised brand mark. */
  logoUrl?: string
  size?: number
  /** Extra class on the tile (`oauth-mark` on the consent screen). */
  className?: string
  /** Shown for unknown clients without an image; defaults to the generic application icon. */
  fallback?: ReactNode
}

/**
 * An application's logo tile: its own image, else the brand mark of a recognised well-known client (Claude, Cursor, …;
 * see lib/known-apps.ts) on a neutral tile, else the generic application icon. The tile is `size` square; consumers
 * set the corner radius (the reference app: 6px in lists, 12px on the 56px consent tile).
 */
export function AppLogo({ name, redirectUris, logoUrl, size = 32, className, fallback }: AppLogoProps) {
  const known = logoUrl ? undefined : matchKnownApp({ name, redirectUris })
  const kind = logoUrl ? 'is-image' : known ? 'is-known' : 'is-placeholder'
  const classes = ['app-logo', kind, className].filter(Boolean).join(' ')
  const style = { width: size, height: size }
  if (logoUrl) return <span className={classes} style={style}><img src={logoUrl} alt={name ?? ''}/></span>
  if (known) return <span className={classes} style={style}><KnownAppMark app={known} size={Math.round(size * 0.6)}/></span>
  return <span className={classes} style={style} aria-hidden="true">{fallback ?? <ApplicationIconPlaceholder/>}</span>
}

/**
 * A row icon for a client known only by name (a member's authorization): the brand mark of a recognised app, else
 * `fallback`. The row's icon slot draws the tile.
 */
export function KnownAppIcon({ name, redirectUris, size = 16, fallback }: KnownAppClient & { size?: number; fallback: ReactNode }) {
  const known = matchKnownApp({ name, redirectUris })
  return known ? <KnownAppMark app={known} size={size}/> : <>{fallback}</>
}
