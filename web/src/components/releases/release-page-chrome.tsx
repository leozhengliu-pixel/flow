import * as Dialog from '@radix-ui/react-dialog'
import { ChevronRight, Menu, X } from 'lucide-react'
import { Fragment, forwardRef, type ComponentPropsWithoutRef, type ReactNode } from 'react'

import { AppLink } from '@/components/ui/app-link'
import { ScopedFlowTooltip } from '@/components/ui/tooltip'
import { shortcutSteps } from '@/components/ui/menu-shortcuts'
import { isMacPlatform } from '@/components/project-detail/project-detail-shortcuts'
import { useI18n } from '@/i18n/i18n'

/** 44px content header shared by the Releases views (title row with trailing actions). */
export function ReleasesHeader({ children, actions, onOpenSidebar }: { children: ReactNode; actions?: ReactNode; onOpenSidebar: () => void }) {
  const { t } = useI18n()
  return <header className="flow-releases-header">
    <button className="flow-releases-header__sidebar" aria-label={t('Open sidebar')} data-sidebar-trigger onClick={onOpenSidebar} type="button"><Menu/></button>
    <div className="flow-releases-header__title">{children}</div>
    {actions && <div className="flow-releases-header__actions">{actions}</div>}
  </header>
}

export type ReleaseCrumb = { label: ReactNode; href?: string; onClick?: () => void; icon?: ReactNode; translate?: boolean }

/** "Releases › Pipeline › ◌ Release" — every crumb but the last is a link. */
export function ReleaseBreadcrumbs({ crumbs }: { crumbs: ReleaseCrumb[] }) {
  const { t } = useI18n()
  return <nav className="flow-releases-crumbs" aria-label={t('Breadcrumb')}>
    {crumbs.map((crumb, index) => {
      const last = index === crumbs.length - 1
      const body = <>{crumb.icon && <span className="flow-releases-crumbs__icon">{crumb.icon}</span>}<span className="flow-releases-crumbs__label" data-i18n-ignore={crumb.translate ? undefined : ''}>{crumb.label}</span></>
      return <Fragment key={index}>
        {index > 0 && <ChevronRight className="flow-releases-crumbs__chevron" aria-hidden="true"/>}
        {last ? <h1 className="flow-releases-crumbs__item" aria-current="page">{body}</h1>
          : crumb.href ? <AppLink className="flow-releases-crumbs__item is-link" href={crumb.href} onClick={event => { if (!crumb.onClick || event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return; event.preventDefault(); crumb.onClick() }}>{body}</AppLink>
            : <button className="flow-releases-crumbs__item is-link" onClick={crumb.onClick} type="button">{body}</button>}
      </Fragment>
    })}
  </nav>
}

type HeaderButtonProps = { label: string; shortcut?: string; tooltip?: string; children: ReactNode } & Omit<ComponentPropsWithoutRef<'button'>, 'aria-label' | 'children'>

/** Transparent 28px round header control (☆, ⋯, +) with Linear's tooltip. */
export const HeaderIconButton = forwardRef<HTMLButtonElement, HeaderButtonProps>(function HeaderIconButton({ label, shortcut, tooltip, children, className, ...props }, ref) {
  const button = <button {...props} ref={ref} aria-label={label} className={`flow-releases-header-button${className ? ` ${className}` : ''}`} type="button">{children}</button>
  return tooltip === '' ? button : <ScopedFlowTooltip label={tooltip ?? label} shortcut={shortcut}>{button}</ScopedFlowTooltip>
})

/** Toolbar pill (display options, filter, details toggle) — Flow's shared `ui-pill`. */
export const ToolbarPillButton = forwardRef<HTMLButtonElement, HeaderButtonProps>(function ToolbarPillButton({ label, shortcut, tooltip, children, className, ...props }, ref) {
  const button = <button {...props} ref={ref} aria-label={label} className={`flow-releases-toolbar-button ui-pill${className ? ` ${className}` : ''}`} type="button">{children}</button>
  return <ScopedFlowTooltip label={tooltip ?? label} shortcut={shortcut}>{button}</ScopedFlowTooltip>
})

/** Key chips inside a primary button ("Add issues ⌥ R"). */
export function ButtonKeys({ value }: { value: string }) {
  const steps = shortcutSteps(value, isMacPlatform())
  return <span className="flow-releases-button-keys" aria-hidden="true">{steps.flat().map((key, index) => <kbd key={index}>{key}</kbd>)}</span>
}

export type EmptyAction = { label: string; onClick?: () => void; href?: string; shortcut?: string }

/** Linear's view empty state: art, title, paragraphs, primary + secondary actions. */
export function ReleaseEmptyState({ art, title, paragraphs, primary, secondary, extra }: { art?: ReactNode; title: ReactNode; paragraphs: ReactNode[]; primary?: EmptyAction; secondary?: EmptyAction; extra?: ReactNode }) {
  const { t } = useI18n()
  return <div className="flow-releases-empty" role="status">
    <div className="flow-releases-empty__body">
      {art && <div className="flow-releases-empty__art">{art}</div>}
      <strong className="flow-releases-empty__title">{title}</strong>
      {paragraphs.map((paragraph, index) => <p key={index}>{paragraph}</p>)}
      {(primary || secondary || extra) && <div className="flow-releases-empty__actions">
        {primary && <button className="flow-releases-primary" onClick={primary.onClick} type="button">{t(primary.label)}{primary.shortcut && <ButtonKeys value={primary.shortcut}/>}</button>}
        {secondary && (secondary.href
          ? <a className="flow-releases-secondary" href={secondary.href} rel="noreferrer" target="_blank">{t(secondary.label)}</a>
          : <button className="flow-releases-secondary" onClick={secondary.onClick} type="button">{t(secondary.label)}</button>)}
        {extra}
      </div>}
    </div>
  </div>
}

/** Small modal used by link/document pickers. */
export function ReleaseBasicDialog({ title, onClose, children, className = '' }: { title: ReactNode; onClose: () => void; children: ReactNode; className?: string }) {
  const { t } = useI18n()
  return <Dialog.Root open onOpenChange={open => { if (!open) onClose() }}>
    <Dialog.Portal>
      <Dialog.Overlay data-flow-motion="backdrop" className="flow-release-dialog-overlay"/>
      <Dialog.Content data-flow-motion="dialog" aria-describedby={undefined} className={`flow-release-basic-dialog ${className}`}>
        <Dialog.Title>{title}</Dialog.Title>
        <Dialog.Close aria-label={t('Close')} className="flow-release-basic-dialog__close"><X/></Dialog.Close>
        {children}
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>
}

export const RELEASE_DOCS_URL = 'https://flow.app/docs/releases'
