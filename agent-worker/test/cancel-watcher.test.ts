import assert from 'node:assert/strict'
import { test } from 'node:test'
import { CancelWatcher } from '../src/runtime-client.js'

test('cancel watcher polls only while runs are held, stops each run once, retries after errors', async () => {
  const held = new Set<string>()
  const calls: string[][] = []
  const answers: Array<() => Promise<{ cancel: string[] }>> = [
    async () => { throw new Error('connection refused') },
    async () => ({ cancel: ['run_a'] }),
    async () => ({ cancel: ['run_a', 'run_b'] }), // run_a is already stopping: not reported again
  ]
  let blockRelease: (() => void) | undefined
  const client = {
    cancels: async (_waitMs: number, ignore: string[] = [], signal?: AbortSignal) => {
      calls.push(ignore)
      const answer = answers.shift()
      if (answer) return answer()
      // Idle long poll: hangs until the watcher closes.
      return new Promise<{ cancel: string[] }>((_, reject) => {
        blockRelease = () => reject(new Error('aborted'))
        signal?.addEventListener('abort', () => reject(new Error('aborted')))
      })
    },
  }
  const stopped: string[] = []
  const watcher = new CancelWatcher(client, () => held, runId => stopped.push(runId), { waitMs: 20_000, retryMs: 5 })
  const running = watcher.run()
  await new Promise(resolve => setTimeout(resolve, 20))
  assert.equal(calls.length, 0, 'no poll without held runs')

  held.add('run_a').add('run_b')
  watcher.started()
  const started = Date.now()
  while (stopped.length < 2 && Date.now() - started < 2000) await new Promise(resolve => setTimeout(resolve, 5))
  assert.deepEqual(stopped, ['run_a', 'run_b'])
  assert.deepEqual(calls[2], ['run_a'], 'runs already stopping are ignored by the server')

  watcher.finished('run_a')
  watcher.close()
  blockRelease?.()
  await running
})
