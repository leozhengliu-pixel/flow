/**
 * Targeted checks (per protocol, FLOW_WORKER_PROTOCOL):
 *   parallel        one prompt that should fan out; reports calls per model turn and whether arguments survived
 *   suspend <id>    process A: asks (ask_user) whether to delete a fixture's sub-issues, then exits on the question
 *                   (state in store)
 *   resume <id>     process B: answers Delete and finishes the run suspended by process A
 *   suspend-resume  runs suspend and resume as two child processes and verifies the result through MCP
 *   stop-write      stops the moment the first write is issued, then asks a follow-up in the same session
 */
import { execFileSync } from 'node:child_process'
import { formatActivity, type FlowActivity } from './activities.js'
import { loadConfig } from './config.js'
import { FlowMcp } from './flow-client.js'
import { flowSystemPrompt } from './prompt.js'
import { FlowAgentWorker } from './worker.js'

const [command, arg] = process.argv.slice(2)
const config = loadConfig()
const worker = new FlowAgentWorker(config)
const mcp = new FlowMcp(config.flowUrl, config.flowToken)
const tag = `QA spike – ${config.protocol === 'openai-chat' ? 'chat' : config.protocol === 'anthropic' ? 'anth' : 'resp'}`
const systemPrompt = flowSystemPrompt({ workspace: 'Dev Workspace (teams DEV, ALPHA)', userName: 'Dev User', today: new Date().toISOString().slice(0, 10) })
const t0 = Date.now()
const log = (a: FlowActivity) => { if (a.type !== 'text.delta') console.error(formatActivity(a, t0)) }

async function fixtureWithChildren(n: number) {
  const parent = await mcp.call('save_issue', { team: 'DEV', title: `${tag} checks parent` })
  for (let i = 1; i <= n; i++) await mcp.call('save_issue', { team: 'DEV', parentId: parent.id, title: `${tag} checks child ${i}` })
  return parent as { id: string; identifier: string }
}

async function children(parentId: string) {
  return ((await mcp.call('list_issues', { parentId, limit: 50 })).items ?? []) as any[]
}

switch (command) {
  case 'parallel': {
    const session = worker.session({ sessionId: `checks-parallel-${t0}`, systemPrompt })
    const out = await session.send('In one step, look up DEV-1, DEV-2 and DEV-3 with get_issue (three parallel calls), and also list the teams. Then give me each issue title in one line.', log)
    await session.close()
    const turns = out.modelCalls.map(call => call.functionCalls.map(fc => `${fc.name}(${fc.arguments})`))
    const lost = out.modelCalls.flatMap(call => call.functionCalls).filter(fc => fc.name === 'get_issue' && !/DEV-\d/.test(fc.arguments)).length
    console.log(JSON.stringify({ protocol: config.protocol, status: out.status, turns, maxCallsPerTurn: Math.max(...out.modelCalls.map(c => c.functionCalls.length)), lostArguments: lost, toolErrors: out.toolCalls.filter(t => t.isError).length }, null, 2))
    break
  }
  case 'suspend': {
    const parent = await fixtureWithChildren(3)
    const session = worker.session({ sessionId: arg, systemPrompt })
    const out = await session.send(`Use ask_user to ask me whether to delete all sub-issues of ${parent.identifier} (options Delete and Keep), then do what I pick.`, log)
    console.log(JSON.stringify({ pid: process.pid, parent: parent.identifier, parentId: parent.id, status: out.status, elicitation: out.elicitation, remaining: (await children(parent.id)).length, stateBytes: worker.store.loadSuspended(arg)?.state.length }))
    await session.close()
    break
  }
  case 'resume': {
    const session = worker.session({ sessionId: arg, systemPrompt })
    const started = Date.now()
    let firstEvent = 0
    const out = await session.resume('Delete', a => { firstEvent ||= Date.now(); log(a) })
    console.log(JSON.stringify({ pid: process.pid, status: out.status, body: out.body, restoreToFirstActivityMs: firstEvent - started, toolCalls: out.toolCalls }))
    await session.close()
    break
  }
  case 'suspend-resume': {
    const id = `checks-suspend-${t0}`
    const node = (sub: string) => JSON.parse(execFileSync(process.execPath, [...process.execArgv, process.argv[1], sub, id], { env: process.env, encoding: 'utf8', stdio: ['ignore', 'pipe', 'inherit'] }).trim().split('\n').at(-1)!)
    const a = node('suspend')
    const b = node('resume')
    const remaining = (await children(a.parentId)).length
    console.log(JSON.stringify({ protocol: config.protocol, processA: { pid: a.pid, status: a.status, question: a.elicitation?.body, remainingBeforeAnswer: a.remaining, stateBytes: a.stateBytes }, processB: { pid: b.pid, status: b.status, restoreToFirstActivityMs: b.restoreToFirstActivityMs, deletesRun: b.toolCalls.length }, remainingAfter: remaining, pass: a.status === 'awaiting' && a.remaining === 3 && b.status === 'response' && remaining === 0 }, null, 2))
    break
  }
  case 'stop-write': {
    const parent = await fixtureWithChildren(0)
    const session = worker.session({ sessionId: `checks-stop-${t0}`, systemPrompt })
    let stopAt = 0
    const out = await session.send(`Create 5 sub-issues under ${parent.identifier} titled "${tag} stop N" for N = 1..5, one per turn.`, a => {
      log(a)
      if (!stopAt && a.type === 'action' && a.action === 'save_issue') { stopAt = Date.now(); session.stop() }
    })
    const created = (await children(parent.id)).length
    const follow = await session.send('What exactly did you create before I stopped you? Do not change anything.', log)
    await session.close()
    console.log(JSON.stringify({ protocol: config.protocol, status: out.status, stopToEndMs: out.endedAt - stopAt, createdBeforeStop: created, followUp: follow.status, followUpBody: follow.body }, null, 2))
    break
  }
  default:
    console.error('usage: checks.ts parallel | suspend-resume | stop-write')
    process.exit(2)
}
