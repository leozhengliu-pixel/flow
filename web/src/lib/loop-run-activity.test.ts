import { describe, expect, it } from 'vitest'

import { inboxRealtimeRelevant } from './inbox-unread'
import { isLoopRunSignal, loopRunActivity } from './loop-run-activity'

describe('loop run realtime signals', () => {
  it('recognizes run signals and reads their payload', () => {
    expect(isLoopRunSignal('loop_run.progress')).toBe(true)
    expect(isLoopRunSignal('loop_run.finished')).toBe(true)
    // Loop definition changes still reload the loop list.
    expect(isLoopRunSignal('loop.run_started')).toBe(false)
    expect(loopRunActivity({ type: 'loop_run.progress', payload: { loopId: 'loop-1', runId: 'run-1', status: 'running', extra: 1 } }))
      .toEqual({ type: 'loop_run.progress', loopId: 'loop-1', runId: 'run-1', status: 'running' })
    expect(loopRunActivity({ type: 'loop_run.finished' })).toEqual({ type: 'loop_run.finished', loopId: undefined, runId: undefined, status: undefined })
  })

  it('does not refresh the inbox for run progress', () => {
    expect(inboxRealtimeRelevant({ type: 'loop_run.progress' }, 'usr_admin')).toBe(false)
    expect(inboxRealtimeRelevant({ type: 'issue.updated', actorId: 'someone' }, 'usr_admin')).toBe(true)
  })
})
