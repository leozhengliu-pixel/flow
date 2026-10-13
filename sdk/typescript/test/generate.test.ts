import { execFileSync } from 'node:child_process'
import { describe, expect, it } from 'vitest'
import { operations } from '../src/generated/index.js'

describe('generated code', () => {
  it('is up to date with docs/openapi.json', () => {
    const output = execFileSync(process.execPath, [new URL('../scripts/generate.mjs', import.meta.url).pathname, '--check'], { encoding: 'utf8' })
    expect(output).toContain('up to date')
  })

  it('covers the typed operations the client relies on', () => {
    for (const name of ['getViewer', 'getIssueRecords', 'postIssues', 'putCustomers', 'postIssuesIdLinkAttachments', 'postFiles', 'getFilesSign'] as const) {
      expect(operations[name]).toBeDefined()
    }
    expect(operations.putCustomers.successStatuses).toEqual([200, 201])
  })
})
