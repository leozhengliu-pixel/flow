import { describe, expect, it } from 'vitest'
import { formatFileSize } from './format-file-size'

describe('formatFileSize', () => {
  it('matches the reference card format', () => {
    expect(formatFileSize(1)).toBe('1 B')
    expect(formatFileSize(1000)).toBe('1,000 B')
    expect(formatFileSize(1023)).toBe('1,023 B')
    expect(formatFileSize(1536)).toBe('1.5 KB')
    expect(formatFileSize(12636)).toBe('12.34 KB')
    expect(formatFileSize(102861)).toBe('100.45 KB')
    expect(formatFileSize(122880)).toBe('120 KB')
    expect(formatFileSize(1048576)).toBe('1 MB')
    expect(formatFileSize(1900000)).toBe('1.81 MB')
    expect(formatFileSize(2411725)).toBe('2.3 MB')
    expect(formatFileSize(5820000)).toBe('5.55 MB')
    expect(formatFileSize(3 * 1024 ** 3)).toBe('3 GB')
  })

  it('picks the unit from the byte count, as the reference helper does, and has no TB step', () => {
    expect(formatFileSize(1024)).toBe('1 KB')
    expect(formatFileSize(1048575)).toBe('1,024 KB')
    expect(formatFileSize(1048576)).toBe('1 MB')
    expect(formatFileSize(1073741823)).toBe('1,024 MB')
    expect(formatFileSize(1073741824)).toBe('1 GB')
    expect(formatFileSize(1610612736)).toBe('1.5 GB')
    expect(formatFileSize(1024 ** 4)).toBe('1,024 GB')
    expect(formatFileSize(5 * 1024 ** 4)).toBe('5,120 GB')
  })

  it('treats missing or invalid sizes as 0 B', () => {
    expect(formatFileSize(0)).toBe('0 B')
    expect(formatFileSize(Number.NaN)).toBe('0 B')
    expect(formatFileSize(-5)).toBe('0 B')
  })
})
