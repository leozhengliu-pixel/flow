/**
 * LS-0374 LazyFastTriageAcceptEditorLoader — React.lazy + preload for FastTriageAcceptEditor.
 */
import { Suspense, type ComponentProps, type ReactNode } from 'react'
import { lazyPage } from '@/lib/lazy-page'

export const LazyFastTriageAcceptEditor = lazyPage(
  () => import('./fast-triage-accept-editor'),
  'FastTriageAcceptEditor',
)

export async function preloadFastTriageAcceptEditor(): Promise<void> {
  await LazyFastTriageAcceptEditor.preload()
}

export type LazyFastTriageAcceptEditorLoaderProps = ComponentProps<
  typeof LazyFastTriageAcceptEditor
> & {
  fallback?: ReactNode
}

/** Host that suspends into the accept editor chunk. */
export function LazyFastTriageAcceptEditorLoader({
  fallback = null,
  ...props
}: LazyFastTriageAcceptEditorLoaderProps) {
  return (
    <Suspense fallback={fallback}>
      <LazyFastTriageAcceptEditor {...props} />
    </Suspense>
  )
}

export default LazyFastTriageAcceptEditorLoader
