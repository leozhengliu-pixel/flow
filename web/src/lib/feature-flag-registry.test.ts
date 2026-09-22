import { afterEach, describe, expect, it } from 'vitest'
import {
  clearFeatureFlagOverrides,
  resolveFeatureFlagEnabled,
  setFeatureFlagOverride,
  buildRegistryFromWorkspace,
  DEFAULT_FEATURE_FLAG_REGISTRY,
} from './feature-flag-registry'

afterEach(() => {
  clearFeatureFlagOverrides()
  localStorage.clear()
})

describe('feature-flag-registry (LS-0263)', () => {
  it('resolves workspace defaults and local overrides', () => {
    const def = DEFAULT_FEATURE_FLAG_REGISTRY.find(item => item.key === 'loops')!
    expect(resolveFeatureFlagEnabled(def, { loops: false }).enabled).toBe(false)
    setFeatureFlagOverride('loops', true)
    expect(resolveFeatureFlagEnabled(def, { loops: false })).toEqual({
      enabled: true,
      overridden: true,
    })
    setFeatureFlagOverride('loops', null)
    expect(resolveFeatureFlagEnabled(def, { loops: false }).overridden).toBe(false)
  })

  it('includes unknown workspace flags in registry', () => {
    const registry = buildRegistryFromWorkspace({ 'custom-flag': true })
    expect(registry.some(item => item.key === 'custom-flag')).toBe(true)
  })
})
