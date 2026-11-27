import { expect, it } from 'vitest'
import { releaseQueryWindowBytes, retainedQueryWindowBytes, retainQueryWindowBytes, QUERY_DIRECTORY_MAX_BYTES } from './query-window-budget'

it('shares one byte budget across catalogs and windows and releases navigation owners', () => {
  const catalog = {}, page = {}, picker = {}
  expect(retainQueryWindowBytes(catalog, 4 * 1024 * 1024)).toBe(true)
  expect(retainQueryWindowBytes(page, 3 * 1024 * 1024)).toBe(true)
  expect(retainQueryWindowBytes(picker, 2 * 1024 * 1024)).toBe(false)
  expect(retainedQueryWindowBytes()).toBe(7 * 1024 * 1024)
  releaseQueryWindowBytes(page)
  for (let navigation = 0; navigation < 100; navigation++) {
    const owner = {}
    expect(retainQueryWindowBytes(owner, 3 * 1024 * 1024)).toBe(true)
    expect(retainedQueryWindowBytes()).toBeLessThanOrEqual(QUERY_DIRECTORY_MAX_BYTES)
    releaseQueryWindowBytes(owner)
  }
  releaseQueryWindowBytes(catalog)
  expect(retainedQueryWindowBytes()).toBe(0)
})
