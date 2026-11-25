import type { CDPSession, Page } from '@playwright/test'
import { closeSync, openSync, writeSync } from 'node:fs'
import { loadavg } from 'node:os'

export type ScaleP19Retention = {
  windows: number; pages: number; rows: number; maxIndex: number; cursors: number; offsets: number
  records: number; pins: number; listeners: number; selected: string[]; directoryBytes: number; summaryBytes: number
}

export function scaleP19CheckRetention(value: ScaleP19Retention, navigation: number, listeners: number) {
  if (value.windows !== 1 || value.pages > 4 || value.rows > 400 || value.maxIndex !== navigation || value.cursors > navigation + 2 || value.offsets !== value.cursors || value.records > 400 || value.pins > 400 || value.listeners > listeners || value.selected.length !== 1 || value.selected[0] !== 'p19-project-0' || value.directoryBytes > 8 * 1024 * 1024 || value.summaryBytes > 16 * 1024 * 1024) throw new Error(`Project retention exceeded its bounds at navigation ${navigation}: ${JSON.stringify(value)}`)
}

export function scaleP19HeapTrend(samples: Array<{ navigation: number; usedSize: number }>) {
  if (samples.length !== 5 || samples.some((sample, index) => sample.navigation !== (index + 1) * 20 || !Number.isFinite(sample.usedSize))) throw new Error('Five finite heap samples at 20/40/60/80/100 are required')
  const intervals = samples.slice(1).map((sample, index) => (sample.usedSize - samples[index].usedSize) / (sample.navigation - samples[index].navigation))
  // Require sustained acceleration across both later intervals; one DEV stack peak or valley is insufficient.
  const earlyBytesPerNavigation = Math.max(0, ...intervals.slice(0, 2))
  const lateBytesPerNavigation = Math.max(0, Math.min(...intervals.slice(2)))
  return { intervals, earlyBytesPerNavigation, lateBytesPerNavigation, allowedBytesPerNavigation: earlyBytesPerNavigation + 32 * 1024 }
}

export async function scaleP19RetentionProbe(cdp: CDPSession) {
  const group = 'scale-p19-retention'
  const instances = async (module: string, name: string) => {
    const prototype = await cdp.send('Runtime.evaluate', { expression: `import(${JSON.stringify(module)}).then(module => module[${JSON.stringify(name)}].prototype)`, awaitPromise: true, objectGroup: group })
    if (prototype.exceptionDetails || !prototype.result.objectId) throw new Error(`Could not inspect ${name}`)
    return (await cdp.send('Runtime.queryObjects', { prototypeObjectId: prototype.result.objectId, objectGroup: group })).objects.objectId
  }
  const windows = await instances('/src/lib/scale-p19-project-window.ts', 'ScaleP19ProjectWindow')
  const clients = await instances('/src/lib/sync-client.ts', 'SyncClient')
  const budget = await cdp.send('Runtime.evaluate', { expression: `import('/src/lib/scale-p19-budget.ts').then(module => module.retainedScaleP19Bytes)`, awaitPromise: true, objectGroup: group })
  const holder = await cdp.send('Runtime.callFunctionOn', { objectId: windows, functionDeclaration: 'function (clients, budget) { return { windows: this, clients, budget }; }', arguments: [{ objectId: clients }, { objectId: budget.result.objectId }], objectGroup: group })
  const read = async () => {
    const result = await cdp.send('Runtime.callFunctionOn', {
      objectId: holder.result.objectId,
      functionDeclaration: `function () {
        const sum = (values, read) => values.reduce((total, value) => total + read(value), 0);
        const pages = this.windows.flatMap(window => [...window.pages.values()]);
        return { windows: this.windows.length, pages: pages.length, rows: sum(pages, page => page.items.length),
          maxIndex: Math.max(...pages.map(page => page.index)), cursors: sum(this.windows, window => window.cursors.size),
          offsets: sum(this.windows, window => window.offsets.size), records: sum(this.clients, client => client.records.size),
          pins: sum(this.clients, client => client.pinned.size), listeners: sum(this.clients, client => client.listeners.size),
          selected: this.windows.flatMap(window => [...window.selected.keys()]), directoryBytes: this.budget(),
          summaryBytes: sum(this.clients, client => client.summaryBytes - client.detailBytes) };
      }`,
      returnByValue: true,
    })
    if (result.exceptionDetails || !result.result.value) throw new Error('Could not inspect project retention')
    return result.result.value as ScaleP19Retention
  }
  return { read, dispose: () => cdp.send('Runtime.releaseObjectGroup', { objectGroup: group }) }
}

export async function scaleP19CollectHeap(page: Page, cdp: CDPSession, passes = 1) {
  const readings = []
  for (let pass = 0; pass < passes; pass++) {
    await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
    await cdp.send('HeapProfiler.collectGarbage')
    readings.push(await cdp.send('Runtime.getHeapUsage'))
  }
  return { readings, dom: await cdp.send('Memory.getDOMCounters'), load: loadavg() }
}

export async function scaleP19HeapSnapshot(cdp: CDPSession, path: string) {
  const file = openSync(path, 'w')
  const write = ({ chunk }: { chunk: string }) => {
    const buffer = Buffer.from(chunk)
    for (let offset = 0; offset < buffer.length;) offset += writeSync(file, buffer, offset, buffer.length - offset)
  }
  cdp.on('HeapProfiler.addHeapSnapshotChunk', write)
  try { await cdp.send('HeapProfiler.takeHeapSnapshot', { reportProgress: false }) }
  finally { cdp.off('HeapProfiler.addHeapSnapshotChunk', write); closeSync(file) }
}

export async function scaleP19HeapOwners(cdp: CDPSession) {
  const result: Record<string, unknown> = {}
  const classes = [
    ['/src/lib/scale-p19-project-window.ts', 'ScaleP19ProjectWindow'],
    ['/src/lib/sync-client.ts', 'SyncClient'],
    ['/src/lib/query-window-store.ts', 'QueryWindowStore'],
    ['/src/lib/record-loader.ts', 'RecordLoader'],
  ]
  try {
    for (const [module, name] of classes) {
      const prototype = await cdp.send('Runtime.evaluate', { expression: `import(${JSON.stringify(module)}).then(module => module[${JSON.stringify(name)}].prototype)`, awaitPromise: true, objectGroup: 'scale-p19-owners' })
      if (prototype.exceptionDetails || !prototype.result.objectId) throw new Error(`Could not inspect ${name}`)
      const objects = await cdp.send('Runtime.queryObjects', { prototypeObjectId: prototype.result.objectId, objectGroup: 'scale-p19-owners' })
      const sizes = await cdp.send('Runtime.callFunctionOn', {
        objectId: objects.objects.objectId,
        functionDeclaration: `function () {
          return Array.from(this, owner => ({ retainedBytes: owner.retainedBytes, ...Object.fromEntries(Object.entries(owner).flatMap(([key, value]) => {
            if (value instanceof Map || value instanceof Set) return [[key, value.size]];
            if (key === 'bytes' || key === 'summaryBytes' || key === 'detailBytes') return [[key, value]];
            return [];
          })) }));
        }`,
        returnByValue: true,
      })
      if (sizes.exceptionDetails) throw new Error(`Could not inspect ${name} fields`)
      result[name] = sizes.result.value
    }
    const budget = await cdp.send('Runtime.evaluate', { expression: `import('/src/lib/scale-p19-budget.ts').then(module => module.retainedScaleP19Bytes())`, awaitPromise: true, returnByValue: true, objectGroup: 'scale-p19-owners' })
    if (budget.exceptionDetails) throw new Error('Could not inspect directory budget')
    result.directoryBytes = budget.result.value
    return result
  } finally { await cdp.send('Runtime.releaseObjectGroup', { objectGroup: 'scale-p19-owners' }) }
}
