import { describe, expect, it } from 'vitest'
import { agentApprovalMarkdown } from './agent-approval-model'

const t = (value: string) => value

describe('agentApprovalMarkdown', () => {
  it('says deletes go to the trash when every target is restorable', () => {
    const text = agentApprovalMarkdown({ kind: 'delete', targets: [{ kind: 'issue', id: 'i1', label: 'ENG-1' }, { kind: 'project', id: 'p1', label: 'Export' }] } as never, t)
    expect(text).toContain('Delete these 2 items? They go to the trash and can be restored.')
    expect(text).not.toContain('(permanent)')
  })

  it('flags permanent deletes (customers, loops) and says they can’t be restored', () => {
    const text = agentApprovalMarkdown({ kind: 'delete', targets: [{ kind: 'customer', id: 'c1', label: 'Acme', permanent: true, affected: { requests: 2 } }, { kind: 'issue', id: 'i1', label: 'ENG-1' }] } as never, t)
    expect(text).toContain('Items marked permanent can’t be restored.')
    expect(text).toContain('- Acme (permanent) (also affects 2 requests)')
    expect(text).toMatch(/- ENG-1$/m)
  })
})
