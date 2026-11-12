import { expect, it } from 'vitest'
import { releaseScaleP19Bytes, retainedScaleP19Bytes, retainScaleP19Bytes, SCALE_P19_DIRECTORY_BYTES } from './scale-p19-budget'

it('scale-p19 shares one byte budget across catalogs and windows and releases navigation owners', () => {
  const catalog = {}, page = {}, picker = {}
  expect(retainScaleP19Bytes(catalog, 4 * 1024 * 1024)).toBe(true)
  expect(retainScaleP19Bytes(page, 3 * 1024 * 1024)).toBe(true)
  expect(retainScaleP19Bytes(picker, 2 * 1024 * 1024)).toBe(false)
  expect(retainedScaleP19Bytes()).toBe(7 * 1024 * 1024)
  releaseScaleP19Bytes(page)
  for (let navigation = 0; navigation < 100; navigation++) {
    const owner = {}
    expect(retainScaleP19Bytes(owner, 3 * 1024 * 1024)).toBe(true)
    expect(retainedScaleP19Bytes()).toBeLessThanOrEqual(SCALE_P19_DIRECTORY_BYTES)
    releaseScaleP19Bytes(owner)
  }
  releaseScaleP19Bytes(catalog)
  expect(retainedScaleP19Bytes()).toBe(0)
})
