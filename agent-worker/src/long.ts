/**
 * Long conversation + large tool result test (one session, many turns):
 *  - LONG_TURNS turns mixing reads and writes on a fixture; per turn: latency, model calls, prompt tokens (and
 *    cached), history items and bytes in the session store;
 *  - a recall question at the end, checked against Flow state (does the model still know every sub-issue it
 *    created many turns ago?);
 *  - one large tool result (every DEV issue with all fields and descriptions).
 *   LONG_TURNS=24 FLOW_WORKER_PROTOCOL=openai-chat LONG_OUT=out.json npx tsx src/long.ts
 */
import { statSync, writeFileSync } from 'node:fs'
import { loadConfig } from './config.js'
import { FlowMcp } from './flow-client.js'
import { flowSystemPrompt } from './prompt.js'
import { FlowAgentWorker } from './worker.js'

const turnsWanted = Number(process.env.LONG_TURNS ?? 24)
const config = loadConfig()
const mcp = new FlowMcp(config.flowUrl, config.flowToken)
const tag = `QA spike – long${config.protocol.slice(7, 11)}`
const parent = await mcp.call('save_issue', { team: 'DEV', title: `${tag} parent`, description: 'Fixture for the long-conversation test.' })
const worker = new FlowAgentWorker(config)
const sessionId = `long-${config.protocol}-${Date.now()}`
const session = worker.session({ sessionId, systemPrompt: flowSystemPrompt({ workspace: 'Dev Workspace (teams DEV, ALPHA)', userName: 'Dev User', today: new Date().toISOString().slice(0, 10) }) })
const historyFile = `${worker.store.dir}/${sessionId}.jsonl`

const prompts: string[] = []
for (let i = 1; prompts.length < turnsWanted; i++) {
  prompts.push(`Create a sub-issue under ${parent.identifier} titled "${tag} step ${i}" with a one-line description of a CSV export subtask.`)
  prompts.push(i % 2 ? `What is the status and priority of ${parent.identifier}'s newest sub-issue?` : 'Which DEV issues were updated most recently? Top 5, one line each.')
  prompts.push(i % 3 ? `Set the priority of "${tag} step ${i}" to ${['Low', 'Medium', 'High'][i % 3]}.` : `Summarize this conversation so far in two sentences.`)
}
prompts.length = turnsWanted

const rows: any[] = []
for (const [index, prompt] of prompts.entries()) {
  const out = await session.send(prompt)
  const items = (await worker.store.session(sessionId).getItems()).length
  rows.push({ turn: index + 1, status: out.status, ms: out.endedAt - out.startedAt, modelCalls: out.usage.modelCalls, failed: out.usage.failedCalls, promptTokens: out.usage.inputTokens, cached: out.usage.cachedTokens, lastCallPromptTokens: out.modelCalls.at(-1)?.inputTokens ?? 0, toolErrors: out.toolCalls.filter(t => t.isError).length, historyItems: items, historyKb: Math.round(statSync(historyFile).size / 1024) })
  console.error(JSON.stringify(rows.at(-1)))
}

// Recall across the whole conversation, verified against Flow.
const recall = await session.send('List the titles of every sub-issue you created in this conversation, one per line, nothing else.')
const kids = ((await mcp.call('list_issues', { parentId: parent.id, limit: 100 })).items ?? []).map((i: any) => i.title as string)
const recalled = kids.filter((title: string) => recall.body.includes(title.replace(`${tag} `, '')))

// Large tool result.
const big = await session.send('List every issue in the DEV team with identifier, title, status, assignee, priority, labels and the full description. Use one list_issues call with limit 250 and all fields, then tell me how many there are and the three oldest.')
const bigResult = big.toolCalls.length
// The turn after a large result: old results are elided from the model input (history.ts), the store keeps them.
const after = await session.send('Which of those issues has the highest priority? One line.')

const summary = {
  protocol: config.protocol, model: config.model, turns: rows.length,
  allOk: rows.every(r => r.status === 'response'), failures: rows.filter(r => r.status !== 'response').map(r => r.turn),
  latencyMsFirst5: Math.round(rows.slice(0, 5).reduce((s, r) => s + r.ms, 0) / 5), latencyMsLast5: Math.round(rows.slice(-5).reduce((s, r) => s + r.ms, 0) / 5),
  lastCallPromptTokensFirst: rows[0]?.lastCallPromptTokens, lastCallPromptTokensLast: rows.at(-1)?.lastCallPromptTokens,
  cacheShare: Math.round((100 * rows.reduce((s, r) => s + r.cached, 0)) / Math.max(1, rows.reduce((s, r) => s + r.promptTokens, 0))),
  historyItems: rows.at(-1)?.historyItems, historyKb: rows.at(-1)?.historyKb,
  recall: { created: kids.length, recalled: recalled.length, status: recall.status },
  largeResult: { status: big.status, ms: big.endedAt - big.startedAt, toolCalls: bigResult, promptTokens: big.usage.inputTokens, answer: big.body.slice(0, 200) },
  turnAfterLargeResult: { status: after.status, ms: after.endedAt - after.startedAt, lastCallPromptTokens: after.modelCalls.at(-1)?.inputTokens, answer: after.body.slice(0, 160) },
  rows,
}
console.log(JSON.stringify({ ...summary, rows: undefined }, null, 2))
if (process.env.LONG_OUT) writeFileSync(process.env.LONG_OUT, JSON.stringify(summary, null, 2))
await session.close()
