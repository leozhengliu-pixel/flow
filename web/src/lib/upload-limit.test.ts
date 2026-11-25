import { afterEach, describe, expect, it } from 'vitest'
import { agentUploadLimit } from '@/components/agent/agent-attachments'
import { formatUploadLimit, rememberUploadLimit, resetRememberedUploadLimit, uploadLimitBytes, withUploadLimit } from './upload-limit'

describe('upload limit', () => {
  afterEach(() => resetRememberedUploadLimit())

  it('defaults to the server default of 100 MB', () => {
    expect(uploadLimitBytes()).toBe(100 * 1024 * 1024)
    expect(uploadLimitBytes({})).toBe(100 * 1024 * 1024)
    expect(agentUploadLimit({ type: 'application/zip' })).toBe(100 * 1024 * 1024)
    expect(agentUploadLimit({ type: 'image/png' })).toBe(2 * 1024 * 1024)
  })

  it('follows the bootstrap and remembers the last one', () => {
    expect(uploadLimitBytes({ uploadMaxBytes: 5 << 20 })).toBe(5 << 20)
    const data = { uploadMaxBytes: 250 << 20 }
    expect(rememberUploadLimit(data)).toBe(data)
    expect(uploadLimitBytes()).toBe(250 << 20)
    expect(agentUploadLimit({ type: 'video/mp4' })).toBe(250 << 20)
    rememberUploadLimit({ uploadMaxBytes: 0 })
    rememberUploadLimit({})
    expect(uploadLimitBytes()).toBe(250 << 20)
    rememberUploadLimit({ uploadMaxBytes: 1 << 20 })
    expect(agentUploadLimit({ type: 'image/png' })).toBe(1 << 20)
  })

  it('formats limits in MB', () => {
    expect(formatUploadLimit(100 << 20)).toBe('100 MB')
    expect(formatUploadLimit(3 << 19)).toBe('1.5 MB')
    expect(withUploadLimit('文件不能超过 {size}', 250 << 20)).toBe('文件不能超过 250 MB')
  })
})
