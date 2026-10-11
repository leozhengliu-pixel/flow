import type { ReactNode } from 'react'
import { toast as sonnerToast } from 'sonner'
import { ToastCard } from '@/components/ui/toast-card'
import type { ToastContent, ToastEntity, ToastKind, ToastLink } from '@/lib/toast-registry'

export type { ToastEntity, ToastKind, ToastLink }

/** Seconds a notification stays up before it dismisses itself (hover, focus and a hidden tab pause it). */
export const TOAST_DURATION = 9000

export type ToastOptions = {
  /** Toasts with the same id update in place. Without one, identical text is de-duplicated automatically. */
  id?: string | number
  description?: ReactNode
  duration?: number
  /** Replaces the per-kind icon; `false` hides it. */
  icon?: ReactNode | false
  /** The entity the toast is about, shown as a chip that opens it. */
  entity?: ToastEntity
  entities?: ToastEntity[]
  /** Link under the message, e.g. "View release" or "Undo". Runs, then dismisses the toast. */
  action?: ToastLink
  link?: ToastLink
  links?: ToastLink[]
  closeButton?: boolean
  dismissible?: boolean
  onDismiss?: () => void
  onAutoClose?: () => void
}

type Resolver<T, A> = ReactNode | ((value: A) => ReactNode) | T
type PromiseMessages<Data> = {
  loading: ReactNode
  success?: ReactNode | ((data: Data) => ReactNode)
  error?: ReactNode | ((error: unknown) => ReactNode)
  description?: ReactNode | ((value: unknown) => ReactNode)
  finally?: () => void
}

let counter = 0

function primitiveText(value: ReactNode): string | undefined {
  if (value === undefined || value === null || value === false) return ''
  return typeof value === 'string' || typeof value === 'number' ? String(value) : undefined
}

/** A stable id for toasts made only of text, so repeating one replaces it rather than stacking a copy. */
function dedupeId(kind: ToastKind, title: ReactNode, options: ToastOptions): string {
  const parts = [primitiveText(title), primitiveText(options.description)]
  for (const entity of [options.entity, ...(options.entities ?? [])]) if (entity) parts.push(primitiveText(entity.title), entity.identifier ?? '')
  for (const link of [options.action, options.link, ...(options.links ?? [])]) if (link) parts.push(primitiveText(link.label))
  if (parts.some(part => part === undefined)) return `flow-toast-${++counter}`
  return `flow-toast:${kind}:${parts.join('\u0001')}`
}

function show(kind: ToastKind, title: ReactNode, options: ToastOptions = {}): string | number {
  const id = options.id ?? dedupeId(kind, title, options)
  const content: ToastContent = {
    kind,
    title,
    description: options.description,
    icon: options.icon,
    entities: [...(options.entity ? [options.entity] : []), ...(options.entities ?? [])],
    links: [...(options.action ? [options.action] : []), ...(options.link ? [options.link] : []), ...(options.links ?? [])],
    closeButton: options.closeButton ?? true,
  }
  return sonnerToast.custom(toastId => <ToastCard content={content} id={toastId}/>, {
    id,
    duration: options.duration ?? (kind === 'loading' ? Number.POSITIVE_INFINITY : TOAST_DURATION),
    dismissible: options.dismissible,
    onDismiss: options.onDismiss ? () => options.onDismiss?.() : undefined,
    onAutoClose: options.onAutoClose ? () => options.onAutoClose?.() : undefined,
  })
}

function promise<Data>(source: Promise<Data> | (() => Promise<Data>), messages: PromiseMessages<Data>) {
  const id = show('loading', messages.loading, { id: `flow-toast-${++counter}`, description: typeof messages.description === 'function' ? undefined : messages.description })
  const run = typeof source === 'function' ? source() : source
  const resolve = <V,>(message: Resolver<never, V> | undefined, value: V): ReactNode => typeof message === 'function' ? (message as (value: V) => ReactNode)(value) : message as ReactNode
  const settled = run.then(
    data => {
      const message = resolve(messages.success, data)
      if (message === undefined || message === false) sonnerToast.dismiss(id)
      else show('success', message, { id, description: resolve(messages.description as Resolver<never, Data> | undefined, data) })
      return data
    },
    (error: unknown) => {
      const message = resolve(messages.error, error) ?? (error instanceof Error ? error.message : undefined)
      if (message === undefined || message === false) sonnerToast.dismiss(id)
      else show('error', message, { id, description: resolve(messages.description as Resolver<never, unknown> | undefined, error) })
      throw error
    },
  ).finally(() => messages.finally?.())
  return Object.assign({ id }, { unwrap: () => settled })
}

/**
 * Flow's notification API. Calls keep sonner's shape (`toast.success(title, { description, action })`) and add
 * entity chips, links, update-in-place and promise/loading support. Rendering lives in `ToastCard`.
 */
export const toast = Object.assign(
  (title: ReactNode, options?: ToastOptions) => show('default', title, options),
  {
    message: (title: ReactNode, options?: ToastOptions) => show('default', title, options),
    info: (title: ReactNode, options?: ToastOptions) => show('info', title, options),
    success: (title: ReactNode, options?: ToastOptions) => show('success', title, options),
    warning: (title: ReactNode, options?: ToastOptions) => show('warning', title, options),
    error: (title: ReactNode, options?: ToastOptions) => show('error', title, options),
    loading: (title: ReactNode, options?: ToastOptions) => show('loading', title, options),
    promise,
    dismiss: (id?: string | number) => sonnerToast.dismiss(id),
  },
)
