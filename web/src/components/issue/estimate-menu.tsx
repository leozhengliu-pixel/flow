import { estimateBulkValues, estimateLabel, estimatePickerOptions, NO_ESTIMATE } from '@/lib/estimates'
import type { PropertyOption } from '@/components/property/property-menu'
import { EstimateValueIcon, type EstimateSettings } from './estimate-icon'

/** Rows of the estimate picker: "No estimate", then the team's scale ("1 Point", "2 Points", …). */
export function estimateMenuOptions(settings: EstimateSettings): PropertyOption[] {
  return estimatePickerOptions(settings).map(option => ({ id: option.id, label: option.label, icon: <EstimateValueIcon value={option.value}/> }))
}

/**
 * Rows of the bulk "Set estimate…" menu: the picker's rows for the selected
 * teams' scale (scales merged when the teams differ); undefined when none of
 * the teams uses estimates.
 */
export function estimateBulkOptions(scales: readonly EstimateSettings[]): PropertyOption[] | undefined {
  const { values, type, settings } = estimateBulkValues(scales)
  if (!values.length) return undefined
  if (settings) return estimateMenuOptions(settings)
  return [
    { id: String(NO_ESTIMATE), label: 'No estimate', icon: <EstimateValueIcon value={NO_ESTIMATE}/> },
    ...values.map(value => ({ id: String(value), label: estimateLabel(value, type), icon: <EstimateValueIcon value={value}/> })),
  ]
}

/** Props every estimate picker shares: the 228px menu with "Set estimate to…" and the ⇧ E hint. */
export function estimateMenuProps(value: number | undefined | null, settings: EstimateSettings) {
  const hasValue = value !== undefined && value !== null && value >= 0
  return {
    label: 'Estimate',
    selectedId: String(hasValue ? value : NO_ESTIMATE),
    options: estimateMenuOptions(settings),
    searchPlaceholder: hasValue ? 'Change estimate to…' : 'Set estimate to…',
    searchShortcut: '⇧ E',
    surfaceClassName: 'property-command-estimate',
  }
}
