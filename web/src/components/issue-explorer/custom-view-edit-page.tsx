/**
 * LS-0156 CustomViewEditPage — thin shell: clone + useViewPreferences + edit chrome.
 */

import type { ReactNode } from 'react'
import { defaultMyIssuesDisplayOptions } from '@/components/my-issues/my-issues-display-defaults'
import type { MyIssuesDisplayOptions } from '@/components/my-issues/my-issues-surface'
import type { SavedView } from '@/types/flow'
import { EditCustomViewHeader } from './edit-custom-view-header'
import { useViewPreferences } from './use-view-preferences'
import type { SavedViewTarget } from './saved-view-editor'
import type { ViewVisual } from '@/components/views/view-icon-picker'

export type CustomViewEditClone = {
  view: SavedView
  filters: unknown
  display: MyIssuesDisplayOptions
  insights?: Record<string, unknown>
}

export function cloneCustomViewForEdit(view: SavedView): CustomViewEditClone {
  return {
    view: { ...view },
    filters: Array.isArray(view.filters) ? structuredClone(view.filters) : [],
    display: {
      ...defaultMyIssuesDisplayOptions,
      ...(view.display as object | undefined),
      properties: new Set(
        Array.isArray((view.display as { properties?: string[] } | undefined)?.properties)
          ? (view.display as { properties: string[] }).properties
          : [...defaultMyIssuesDisplayOptions.properties],
      ),
    } as MyIssuesDisplayOptions,
    insights: view.insights ? { ...(view.insights as Record<string, unknown>) } : undefined,
  }
}

export function CustomViewEditPage({
  orgKey,
  view,
  saveTargets = [],
  saving = false,
  children,
  onCancel,
  onSave,
}: {
  orgKey: string
  view: SavedView
  saveTargets?: SavedViewTarget[]
  saving?: boolean
  children?: ReactNode
  onCancel: () => void
  onSave: (input: {
    name: string
    description: string
    target: SavedViewTarget | undefined
    visual: ViewVisual
    preferences: ReturnType<ReturnType<typeof useViewPreferences>['snapshot']>
  }) => void
}) {
  const clone = cloneCustomViewForEdit(view)
  const preferences = useViewPreferences({
    view: clone.view,
    fallback: defaultMyIssuesDisplayOptions,
    storageKey: `flow:view-prefs:${orgKey}:${view.id}`,
  })

  return (
    <div data-custom-view-edit-page className="custom-view-edit-page">
      <EditCustomViewHeader
        orgKey={orgKey}
        viewId={view.id}
        initialName={clone.view.name}
        initialDescription={clone.view.description}
        initialIcon={clone.view.icon}
        initialColor={clone.view.color}
        initialTarget={saveTargets[0]}
        saveTargets={saveTargets}
        saving={saving}
        hasChanges
        onCancel={onCancel}
        onSave={(name, description, target, visual) => {
          onSave({
            name,
            description,
            target,
            visual,
            preferences: preferences.snapshot(),
          })
        }}
      />
      {children}
    </div>
  )
}
