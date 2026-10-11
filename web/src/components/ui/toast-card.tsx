import { useEffect, useRef, type KeyboardEvent, type ReactNode } from 'react'
import { toast as sonnerToast } from 'sonner'
import { isMacPlatform } from '@/components/project-detail/project-detail-shortcuts'
import { shortcutSteps } from '@/components/ui/menu-shortcuts'
import { ScopedFlowTooltip } from '@/components/ui/tooltip'
import { ToastCloseIcon, ToastCopyIcon, ToastErrorIcon, ToastInfoIcon, ToastSpinnerIcon, ToastSuccessIcon, ToastWarningIcon } from '@/components/ui/toast-icons'
import { useOptionalTranslate } from '@/i18n/i18n'
import {
  OPEN_TOAST_LINK_SHORTCUT,
  primaryToastTarget,
  registerToastLink,
  runToastLink,
  useIsLatestLinkToast,
  type ToastContent,
  type ToastEntity,
  type ToastKind,
  type ToastLink,
} from '@/lib/toast-registry'

const COPY_TITLE = /\bcop(y|ied)\b/i

/** The leading icon for a toast; `copy` is picked for clipboard confirmations. */
function toastKindIcon(kind: ToastKind, copy: boolean): ReactNode {
  switch (kind) {
    case 'success': return <ToastSuccessIcon/>
    case 'error': return <ToastErrorIcon/>
    case 'warning': return <ToastWarningIcon/>
    case 'loading': return <ToastSpinnerIcon/>
    default: return copy ? <ToastCopyIcon/> : <ToastInfoIcon/>
  }
}

function titleText(title: ReactNode) {
  return typeof title === 'string' ? title : ''
}

/** The body of one notification: icon, message, optional description, entity chips, links and a dismiss button. */
export function ToastCard({ id, content }: { id: string | number; content: ToastContent }) {
  const t = useOptionalTranslate()
  const { kind, title, description, entities, links } = content
  const dismiss = () => { sonnerToast.dismiss(id) }
  const icon = content.icon === false ? null : content.icon ?? toastKindIcon(kind, COPY_TITLE.test(titleText(title)))
  const primary = primaryToastTarget(content)
  const primaryRef = useRef(primary)
  primaryRef.current = primary
  const hasPrimary = Boolean(primary)

  useEffect(() => {
    if (!hasPrimary) return undefined
    return registerToastLink(id, () => { primaryRef.current?.(); sonnerToast.dismiss(id) })
  }, [id, hasPrimary])

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Escape' && !event.defaultPrevented) { event.preventDefault(); dismiss() }
  }

  return (
    <div
      className="flow-toast"
      data-kind={kind}
      data-flow-toast=""
      data-rich={description || entities.length || links.length ? '' : undefined}
      onKeyDown={onKeyDown}
      role={kind === 'error' ? 'alert' : 'status'}
    >
      {icon ? <span aria-hidden="true" className="flow-toast__icon">{icon}</span> : null}
      <div className="flow-toast__body">
        <div className="flow-toast__title">{title}</div>
        {description ? <div className="flow-toast__description">{description}</div> : null}
        {entities.length ? <div className="flow-toast__entities">{entities.map((entity, index) => <ToastChip entity={entity} key={index} onOpen={dismiss}/>)}</div> : null}
        {links.length ? <div className="flow-toast__links">{links.map((link, index) => <ToastLinkButton hint={index === 0 && hasPrimary} id={id} key={index} link={link} onRun={dismiss}/>)}</div> : null}
      </div>
      {content.closeButton ? <button aria-label={t('Dismiss')} className="flow-toast__close" onClick={dismiss} type="button"><ToastCloseIcon/></button> : null}
    </div>
  )
}

function ToastChip({ entity, onOpen }: { entity: ToastEntity; onOpen: () => void }) {
  const inner = <>
    {entity.icon ? <span aria-hidden="true" className="flow-toast__chip-icon">{entity.icon}</span> : null}
    {entity.identifier ? <span className="flow-toast__chip-id" data-i18n-ignore="">{entity.identifier}</span> : null}
    <span className="flow-toast__chip-title" data-i18n-ignore="">{entity.title}</span>
  </>
  if (!entity.onClick && !entity.href) return <span className="flow-toast__chip" data-static="">{inner}</span>
  return <button className="flow-toast__chip" onClick={() => { runToastLink(entity); onOpen() }} type="button">{inner}</button>
}

function ToastLinkButton({ id, link, hint, onRun }: { id: string | number; link: ToastLink; hint: boolean; onRun: () => void }) {
  const t = useOptionalTranslate()
  const latest = useIsLatestLinkToast(id)
  const button = <button className="flow-toast__link" onClick={() => { runToastLink(link); onRun() }} type="button">{link.label}</button>
  if (!hint || !latest) return button
  return <ScopedFlowTooltip contentClassName="flow-toast-tooltip" label={t('Open toast link')} shortcut={shortcutSteps(OPEN_TOAST_LINK_SHORTCUT, isMacPlatform())[0].join(isMacPlatform() ? '' : '+')} side="top">{button}</ScopedFlowTooltip>
}
