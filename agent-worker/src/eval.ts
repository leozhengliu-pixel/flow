/**
 * Eval: the same 15 prompts through
 *   base = today's in-process Flow agent (POST /api/agent/sessions/stream, FLOW_AGENT_PROTOCOL=openai-responses)
 *   resp = this worker, OpenAI Responses (OpenAIResponsesModel)
 *   chat = this worker, OpenAI Chat Completions (OpenAIChatCompletionsModel)
 *   anth = this worker, Anthropic Messages (aisdk + @ai-sdk/anthropic)
 * All arms use the same upstream model (one provider serving all three protocols) and effort and act on their own "QA spike – <arm>" fixtures; results are
 * verified against Flow state through MCP.
 *
 * Env: EVAL_SUITE=core|all (all adds the adapted reference-app prompts), EVAL_REP=n (fixture/tag suffix for repeats),
 *      FLOW_WORKER_FLOW_TOKEN_FILE, EVAL_COOKIE_FILE, EVAL_OUT_DIR, EVAL_ARMS=base,resp,chat,anth, EVAL_ONLY=ids,
 *      FLOW_WORKER_{RESPONSES,CHAT,ANTHROPIC}_BASE_URL,
 *      EVAL_SUSPEND=1 (default) drops the session object on every elicitation and resumes from the store.
 */
import { appendFileSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { formatActivity } from './activities.js'
import { baseURLFor, loadConfig, type Protocol } from './config.js'
import { FlowChatClient, FlowMcp, type BaselineTurn } from './flow-client.js'
import type { ModelCallRecord } from './models.js'
import { flowSystemPrompt } from './prompt.js'
import { FlowAgentWorker, type FlowAgentSession, type TurnOutcome } from './worker.js'

type Arm = 'base' | 'resp' | 'chat' | 'anth'
type Ctx = { arm: Arm; issue: string; issueId: string; other: string; otherId: string; tag: string; mcp: FlowMcp; userId: string }
type Check = { pass: boolean; note: string }
type Case = {
  id: string
  kind: string
  prompt: (c: Ctx) => string
  answer?: string // ask_user answer (option label)
  noAnswer?: boolean // leave the elicitation unanswered (the case ends waiting)
  stopAfterFirstTool?: boolean
  followUp?: string
  verify: (c: Ctx, r: Result) => Promise<Check>
}
export type Result = {
  arm: Arm; id: string; kind: string; status: string; body: string
  totalMs: number; firstActivityMs?: number; firstTextMs?: number
  tools: string[]; toolErrors: number; elicitations: number
  modelCalls?: number; failedModelCalls?: number; inputTokens?: number; cachedTokens?: number; outputTokens?: number; toolsOfferedMean?: number
  parallelTurns?: number; maxParallel?: number; emptyArgCalls?: number
  stopLatencyMs?: number; followUpStatus?: string; followUpBody?: string
  check?: Check
}

const cases: Case[] = [
  { id: 'R1', kind: 'read', prompt: c => `What can you do for me on ${c.issue}? List your capabilities briefly.`,
    verify: async (_c, r) => ({ pass: r.status === 'response' && r.body.length > 40, note: '' }) },
  { id: 'R2', kind: 'read multi-tool', prompt: () => 'How many issues are in Triage right now, which cycles and initiatives exist, and which customers do we track?',
    verify: async (_c, r) => { const need = ['list_cycles', 'list_initiatives', 'list_customers']; const miss = need.filter(n => !r.tools.includes(n)); return { pass: r.status === 'response' && miss.length === 0, note: miss.length ? `missing ${miss.join(',')}` : '' } } },
  { id: 'R3', kind: 'read table', prompt: () => 'Find every issue in the workspace that mentions CSV or export and show them in a table with status, priority, assignee and project.',
    verify: async (c, r) => ({ pass: r.status === 'response' && r.body.includes(c.issue) && /\|/.test(r.body), note: r.body.includes(c.issue) ? '' : `own fixture ${c.issue} missing` }) },
  { id: 'R4', kind: 'read history', prompt: c => `Give me the full change history of ${c.issue} (who changed what, when) as a table.`,
    verify: async (_c, r) => ({ pass: r.status === 'response' && r.tools.includes('list_issue_history'), note: r.tools.includes('list_issue_history') ? '' : 'no list_issue_history' }) },
  { id: 'W1', kind: 'write fields', prompt: c => `On ${c.issue}: set priority to High, add the label Bug, and set an estimate of 3 points.`,
    verify: async c => { const i = await c.mcp.call('get_issue', { id: c.issue }); const labels = (i.labels ?? []).map((l: any) => (typeof l === 'string' ? l : l.name)); const ok = i.priority === 2 && labels.includes('Bug') && i.estimate === 3; return { pass: ok, note: `priority=${i.priority} labels=${labels.join('/')} estimate=${i.estimate}` } } },
  { id: 'W2', kind: 'write multi (3 sub-issues)', prompt: c => `Break ${c.issue} down into 3 sub-issues: backend streaming export job, email-a-download-link, and UI progress indicator. Give each a short description and acceptance criteria. Start each sub-issue title with "QA spike – ${c.tag} ".`,
    verify: async c => { const kids = await children(c); return { pass: kids.length === 3, note: `${kids.length} sub-issues` } } },
  { id: 'W3', kind: 'write assign+state', prompt: c => `Assign ${c.issue} to me and move it to In Progress.`,
    verify: async c => { const i = await c.mcp.call('get_issue', { id: c.issue }); const ok = i.assignee?.id === c.userId && /progress/i.test(i.state?.name ?? i.status ?? ''); return { pass: ok, note: `assignee=${i.assignee?.name ?? '-'} state=${i.state?.name ?? i.status}` } } },
  { id: 'W4', kind: 'write project+milestone+move', prompt: c => `Create a new project called "QA spike – ${c.tag} Export revamp" in the DEV team with a one-paragraph overview, add a milestone "Beta" due in two weeks, and move ${c.issue} into the project under that milestone.`,
    verify: async c => { const i = await c.mcp.call('get_issue', { id: c.issue }); const p = i.project?.name ?? ''; const m = i.projectMilestone?.name ?? i.milestone?.name ?? ''; return { pass: p.includes(`QA spike – ${c.tag} Export revamp`) && /beta/i.test(m), note: `project=${p || '-'} milestone=${m || '-'}` } } },
  { id: 'W5', kind: 'write document', prompt: c => `Write a short technical design document titled "QA spike – ${c.tag} export design" (problem, proposal, rollout, risks) and attach it to the project "QA spike – ${c.tag} Export revamp".`,
    verify: async c => { const d = await c.mcp.call('list_documents', { query: `QA spike – ${c.tag} export design` }); const doc = (d.items ?? [])[0]; const projects: string[] = doc?.projectIds ?? (doc?.project?.id ? [doc.project.id] : []); return { pass: projects.length > 0, note: doc ? `doc ${doc.title} projects=${projects.length} via=${doc.botActor?.name ?? 'user'}` : 'no doc' } } },
  { id: 'Q1', kind: 'ask_user select', answer: 'High', prompt: c => `I want to create a new issue "QA spike – ${c.tag} Dark mode contrast bug" in the DEV team. Before creating it, ask me which priority it should have — give me the choices as options I can click.`,
    verify: async (c, r) => { const s = await c.mcp.call('search_issues', { queries: [`QA spike – ${c.tag} Dark mode`] }); const items = (s.items ?? s.results ?? s).filter?.((x: any) => String(x.title).includes(`QA spike – ${c.tag} Dark mode`)) ?? []; const ok = r.elicitations > 0 && items.some((x: any) => x.priority === 2); return { pass: ok, note: `elicitations=${r.elicitations} created=${items.map((x: any) => `${x.identifier}:p${x.priority}`).join(',') || 'none'}` } } },
  { id: 'A1', kind: 'bulk delete approval', prompt: c => `Delete all sub-issues of ${c.issue}.`,
    verify: async (c, r) => { const kids = await children(c); return { pass: r.status === 'response' && r.elicitations > 0 && kids.length === 0, note: `approvals=${r.elicitations} remaining=${kids.length}` } } },
  { id: 'Z1', kind: 'Chinese + write', prompt: c => `用中文回答：这个工作区里现在最需要关注的三件事是什么？顺便把 ${c.issue} 的优先级改成紧急。`,
    verify: async (c, r) => { const i = await c.mcp.call('get_issue', { id: c.issue }); const zh = /[一-鿿]/.test(r.body); return { pass: i.priority === 1 && zh, note: `priority=${i.priority} chinese=${zh}` } } },
  { id: 'M1', kind: 'multi-step initiative+update', prompt: c => `Create an initiative called "QA spike – ${c.tag} Data portability", link the project "QA spike – ${c.tag} Export revamp" to it, and post an initiative update saying it's On track.`,
    verify: async c => {
      const ini = ((await c.mcp.call('list_initiatives', { query: `QA spike – ${c.tag} Data portability` })).items ?? [])[0]
      const project = ((await c.mcp.call('list_projects', { query: `QA spike – ${c.tag} Export revamp` })).items ?? [])[0]
      const linked = Boolean(ini && (project?.initiatives ?? []).includes(ini.id))
      const updates = ini ? ((await c.mcp.call('get_status_updates', { type: 'initiative', initiative: ini.id })).items ?? []).length : 0
      return { pass: Boolean(ini) && linked && updates > 0, note: `initiative=${ini ? 'yes' : 'no'} linked=${linked} updates=${updates}` }
    } },
  { id: 'N1', kind: 'refusal (no code access)', prompt: () => 'Look at our codebase and tell me which file implements the CSV export endpoint.',
    verify: async (_c, r) => { const fabricated = /\b[\w/-]+\.(go|ts|tsx|js|py)\b/.test(r.body); return { pass: r.status === 'response' && !fabricated, note: fabricated ? 'mentions a file path' : '' } } },
  { id: 'S1', kind: 'stop mid-tool + follow-up', stopAfterFirstTool: true, followUp: 'What did you do so far? Do not change anything.',
    prompt: c => `For each of the 10 most recently updated issues in the workspace, read it with get_issue and then create a sub-issue under ${c.issue} titled "QA spike – ${c.tag} follow-up N" summarizing it. Do them one at a time.`,
    verify: async (c, r) => { const kids = await children(c); return { pass: r.status === 'canceled' && r.followUpStatus === 'response' && kids.length < 10, note: `stop→end ${r.stopLatencyMs}ms, created=${kids.length}, follow-up=${r.followUpStatus}` } } },
]

/**
 * Prompts adapted from the 39 prompts sent to the reference app's agent (scratchpad agent-research prompts, 
 * P1–P39) that the core set does not already cover, rewritten for the chat surface and the arm's fixtures.
 * They run after the core set, in order (L23–L29 use the project and document W4/W5 created, L38 M1's initiative).
 */
const fixtureDescription = 'Exporting more than 10k issues to CSV times out after 30 seconds and the download never starts.'
const project = (c: Ctx) => `QA spike – ${c.tag} Export revamp`
async function getProject(c: Ctx) { return c.mcp.call('get_project', { query: project(c) }) }
const responded = (r: Result) => ({ pass: r.status === 'response', note: '' })
const used = (r: Result, tool: string) => r.tools.includes(tool)
const referenceCases: Case[] = [
  { id: 'L02', kind: 'related / duplicates (P2)', prompt: c => `Find related or duplicate issues for ${c.issue} in the workspace.`,
    verify: async (_c, r) => ({ pass: r.status === 'response' && (used(r, 'search_issues') || used(r, 'list_issues')), note: '' }) },
  { id: 'L03', kind: 'add relation (P3)', prompt: c => `Mark ${c.issue} as related to ${c.other}.`,
    verify: async c => { const i = await c.mcp.call('get_issue', { id: c.issue }); const rel = JSON.stringify(i.relations?.relatedTo ?? []); return { pass: rel.includes(c.other) || rel.includes(c.otherId), note: `relatedTo=${rel.slice(0, 80)}` } } },
  { id: 'L04', kind: 'remove relation + verify (P4/P5)', prompt: c => `Remove the relation between ${c.issue} and ${c.other}, then verify after the change that it is gone.`,
    verify: async (c, r) => { const i = await c.mcp.call('get_issue', { id: c.issue }); const rel = JSON.stringify(i.relations?.relatedTo ?? []); return { pass: r.status === 'response' && !rel.includes(c.other) && !rel.includes(c.otherId), note: `relatedTo=${rel.slice(0, 80)}` } } },
  { id: 'L05', kind: 'fields + verify (P5)', prompt: c => `On ${c.issue}: set priority to Low and add the label Performance (keep the existing labels), then verify the change.`,
    verify: async c => { const i = await c.mcp.call('get_issue', { id: c.issue }); const labels = (i.labels ?? []).map((l: any) => (typeof l === 'string' ? l : l.name)); return { pass: i.priority === 4 && labels.includes('Performance'), note: `priority=${i.priority} labels=${labels.join('/')}` } } },
  { id: 'L07', kind: 'clarify first, no change (P7)', noAnswer: true, prompt: c => `Before rewriting the description of ${c.other}, ask me any clarifying questions you need. Don't change anything yet.`,
    verify: async (c, r) => { const i = await c.mcp.call('get_issue', { id: c.other }); const unchanged = i.description === fixtureDescription; return { pass: (r.status === 'response' || r.status === 'awaiting') && unchanged && (r.status === 'awaiting' || r.body.includes('?')), note: `unchanged=${unchanged} asked=${r.status === 'awaiting' ? 'ask_user' : 'text'}` } } },
  { id: 'L08', kind: 'rewrite description (P8)', prompt: c => `Rewrite the description of ${c.other} with these sections: Reproduction steps (numbered), Expected behavior, Acceptance criteria. Go ahead and change it now.`,
    verify: async c => { const i = await c.mcp.call('get_issue', { id: c.other }); const d = String(i.description ?? ''); return { pass: /acceptance criteria/i.test(d) && /expected/i.test(d), note: `${d.length} chars` } } },
  { id: 'L09', kind: 'web research (P9)', prompt: () => 'Search the web for best practices for exporting very large CSV files and give me a short summary with source links.',
    verify: async (_c, r) => responded(r) },
  { id: 'L10', kind: 'owner + state + reminder (P10)', prompt: c => `Who on the team would be the best owner for ${c.other}? Assign it to them, move it to In Progress, and remind me about it tomorrow at 9am.`,
    verify: async (c, r) => { const i = await c.mcp.call('get_issue', { id: c.other }); const ok = Boolean(i.assignee) && /progress/i.test(i.status ?? '') && used(r, 'create_reminder'); return { pass: ok, note: `assignee=${i.assignee?.name ?? '-'} state=${i.status} reminder=${used(r, 'create_reminder')}` } } },
  { id: 'L14', kind: 'single delete, no approval (P14)', prompt: c => `Create a sub-issue under ${c.other} titled "QA spike – ${c.tag} scope check", then delete it — it's out of scope.`,
    verify: async (c, r) => { const kids = ((await c.mcp.call('list_issues', { parentId: c.otherId, limit: 50 })).items ?? []).filter((i: any) => /scope check/.test(i.title)); return { pass: kids.length === 0 && used(r, 'delete_issue') && r.elicitations === 0, note: `left=${kids.length} approvals=${r.elicitations}` } } },
  { id: 'L19', kind: 'customer + request (P19)', prompt: c => `Create a customer named "QA spike – ${c.tag} Acme Corp" and log a customer request from them on ${c.issue} saying they need exports of 50k issues.`,
    verify: async (c, r) => { const cs = ((await c.mcp.call('list_customers', { query: `QA spike – ${c.tag} Acme` })).items ?? []).filter((x: any) => x.name.includes(`${c.tag} Acme`)); return { pass: cs.length > 0 && used(r, 'save_customer_request') && (cs[0].requestCount ?? 1) > 0, note: `customers=${cs.length} requests=${cs[0]?.requestCount}` } } },
  { id: 'L19b', kind: 'unsupported write: cycle (P19)', prompt: () => 'Can you create a cycle for next week for the DEV team?',
    verify: async (_c, r) => responded(r) },
  { id: 'L21', kind: 'loop, disabled (P21/P22)', prompt: c => `Set up a loop (disabled for now) named "QA spike – ${c.tag} weekly export digest" that runs every Monday at 9am and comments a progress summary on ${c.issue}. Don't enable it.`,
    verify: async (_c, r) => ({ pass: r.status === 'response' && used(r, 'save_loop'), note: '' }) },
  { id: 'L23', kind: 'edit project description (P23)', prompt: c => `Add a "Risks" section with 3 bullets to the description of the project "${project(c)}". Keep the rest.`,
    verify: async c => { const p = await getProject(c); return { pass: /risks/i.test(p.description ?? ''), note: '' } } },
  { id: 'L24', kind: 'review latest update (P24)', prompt: c => `Is the latest status of project "${project(c)}" accurate given the current issue states? Point out anything missing and suggest a better health status if needed. Don't change anything.`,
    verify: async (_c, r) => responded(r) },
  { id: 'L26', kind: 'draft an update (P26)', prompt: c => `Help me write an update for the project "${project(c)}".`,
    verify: async (_c, r) => responded(r) },
  { id: 'L27', kind: 'post update At risk (P27)', prompt: c => `Post a project update on "${project(c)}" with health At risk, two sentences about the current progress.`,
    verify: async c => { const p = await getProject(c); const u = ((await c.mcp.call('get_status_updates', { type: 'project', project: p.id })).items ?? [])[0]; return { pass: u?.health === 'atRisk', note: `latest=${u?.health ?? 'none'}` } } },
  { id: 'L28', kind: 'risk + lead + target date (P28)', prompt: c => `What's the riskiest open item in the project "${project(c)}" and what would you do about it this week? Also set the project lead to me and the target date to Oct 30, 2026.`,
    verify: async c => { const p = await getProject(c); const lead = p.leadId ?? p.lead?.id; return { pass: lead === c.userId && String(p.targetDate ?? '').startsWith('2026-10-30'), note: `lead=${lead ?? '-'} target=${p.targetDate ?? '-'}` } } },
  { id: 'L29', kind: 'edit document in place (P29)', prompt: c => `In the document "QA spike – ${c.tag} export design", rewrite the first paragraph so it also quantifies the user impact, editing the document directly.`,
    verify: async (_c, r) => ({ pass: r.status === 'response' && used(r, 'save_document'), note: '' }) },
  { id: 'L35', kind: 'save a skill (P35)', prompt: c => `Save a personal skill called "QA spike – ${c.tag} attention digest" that lists the three things that most need attention in the workspace.`,
    verify: async c => { const s = ((await c.mcp.call('list_agent_skills', {})).items ?? []).filter((x: any) => String(x.name).includes(`${c.tag} attention digest`)); return { pass: s.length > 0, note: '' } } },
  { id: 'L36', kind: 'capability self-report (P36)', prompt: () => "Which of these can you do here: create views, create labels, create teams, invite members, change workspace settings? Answer as a table. Don't change anything.",
    verify: async (_c, r) => ({ pass: r.status === 'response' && /\|/.test(r.body), note: '' }) },
  { id: 'L37', kind: 'custom view (P37)', prompt: c => `Create a personal custom view named "QA spike – ${c.tag} my urgent work" showing open issues assigned to me with priority High or Urgent, grouped by project.`,
    verify: async c => { const v = ((await c.mcp.call('list_views', { query: `QA spike – ${c.tag} my urgent` })).items ?? []).filter((x: any) => String(x.name).includes(`${c.tag} my urgent`)); return { pass: v.length > 0, note: '' } } },
  { id: 'L38', kind: 'initiative target date + update (P38)', prompt: c => `Post a new update on the initiative "QA spike – ${c.tag} Data portability" summarizing the linked project's progress, and set the initiative's target date to Dec 15, 2026.`,
    verify: async c => { const i = await c.mcp.call('get_initiative', { query: `QA spike – ${c.tag} Data portability` }); return { pass: String(i.targetDate ?? '').startsWith('2026-12-15'), note: `target=${i.targetDate ?? '-'}` } } },
  { id: 'L39', kind: 'bulk cleanup via agent (P39)', prompt: c => `Delete the view "QA spike – ${c.tag} my urgent work" and the skill "QA spike – ${c.tag} attention digest".`,
    verify: async (c, r) => {
      const v = ((await c.mcp.call('list_views', { query: `QA spike – ${c.tag} my urgent` })).items ?? []).filter((x: any) => String(x.name).includes(`${c.tag} my urgent`))
      const s = ((await c.mcp.call('list_agent_skills', {})).items ?? []).filter((x: any) => String(x.name).includes(`${c.tag} attention digest`))
      return { pass: r.status === 'response' && v.length === 0 && s.length === 0, note: `views=${v.length} skills=${s.length} approvals=${r.elicitations}` }
    } },
]

async function children(c: Ctx) {
  const l = await c.mcp.call('list_issues', { parentId: c.issueId, limit: 50 })
  return (l.items ?? []) as any[]
}

const outDir = process.env.EVAL_OUT_DIR ?? join(process.cwd(), '.runtime', 'eval')
mkdirSync(outDir, { recursive: true })
const baseConfig = loadConfig()
const mcp = new FlowMcp(baseConfig.flowUrl, baseConfig.flowToken)
const cookie = process.env.EVAL_COOKIE_FILE ? readFileSync(process.env.EVAL_COOKIE_FILE, 'utf8').trim() : ''
const arms = (process.env.EVAL_ARMS ?? 'base,resp,chat,anth').split(',') as Arm[]
const only = process.env.EVAL_ONLY?.split(',')
const rep = process.env.EVAL_REP ?? ''
const suite = process.env.EVAL_SUITE ?? 'core'
const suspendOnWait = process.env.EVAL_SUSPEND !== '0'
const me = { id: process.env.EVAL_USER_ID ?? 'usr_1790559030485511000' } // dev@flow.test
const today = new Date().toISOString().slice(0, 10)
const protocolOf: Record<Exclude<Arm, 'base'>, Protocol> = { resp: 'openai-responses', chat: 'openai-chat', anth: 'anthropic' }
const workers = new Map<Arm, FlowAgentWorker>()

async function fixture(arm: Arm): Promise<Ctx> {
  const tag = rep ? `${arm}${rep}` : arm
  const created = await mcp.call('save_issue', { team: 'DEV', title: `QA spike – ${tag} CSV export fails for large workspaces`, description: fixtureDescription, priority: 3 })
  const other = await mcp.call('save_issue', { team: 'DEV', title: `QA spike – ${tag} Export button misaligned`, description: fixtureDescription, priority: 3 })
  return { arm, issue: created.identifier, issueId: created.id, other: other.identifier, otherId: other.id, tag, mcp, userId: me.id }
}

function parallelStats(calls: ModelCallRecord[]) {
  const turns = calls.filter(call => call.functionCalls.length > 1)
  // Tools that cannot run without arguments; an empty object there means the arguments were lost in streaming.
  const empty = calls.flatMap(call => call.functionCalls).filter(call => (!call.arguments || call.arguments === '{}') && /^(get_(?!workspace)|save_|delete_|create_|triage_)/.test(call.name))
  return { parallelTurns: turns.length, maxParallel: Math.max(0, ...calls.map(call => call.functionCalls.length)), emptyArgCalls: empty.length }
}

async function runWorker(c: Ctx, k: Case, log: (s: string) => void): Promise<Result> {
  const worker = workers.get(c.arm)!
  const sessionId = `eval-${c.arm}-${k.id}-${Date.now()}`
  const open = () => worker.session({ sessionId, systemPrompt: flowSystemPrompt({ workspace: 'Dev Workspace (teams DEV, ALPHA)', userName: 'Dev User', today }) })
  let session: FlowAgentSession = open()
  const t0 = Date.now()
  let stopAt = 0
  let stopped = false
  const sink = (a: Parameters<typeof formatActivity>[0]) => {
    if (a.type !== 'text.delta') log(formatActivity(a, t0))
    if (k.stopAfterFirstTool && !stopped && a.type === 'action') {
      stopped = true
      setTimeout(() => { stopAt = Date.now(); log('   [host] stop signal'); session.stop() }, 1000)
    }
  }
  const turns: TurnOutcome[] = [await session.send(k.prompt(c), sink)]
  let elicitations = 0
  while (turns.at(-1)!.status === 'awaiting' && elicitations < 3 && !k.noAnswer) {
    const e = turns.at(-1)!.elicitation!
    elicitations++
    const answer = e.kind === 'select' ? (e.options.find(o => o.label.toLowerCase().includes((k.answer ?? '').toLowerCase())) ?? e.options[0])?.label ?? k.answer ?? 'ok' : 'approve'
    log(`   [host] answer ${e.kind} -> ${answer}${suspendOnWait ? ' (session dropped; resumed from the store)' : ''}`)
    if (suspendOnWait) { await session.close(); session = open() }
    turns.push(await session.resume(answer, sink))
  }
  const first = turns[0]
  const last = turns.at(-1)!
  let followUp: TurnOutcome | undefined
  if (k.followUp) followUp = await session.send(k.followUp, sink)
  await session.close()
  const calls = turns.flatMap(t => t.modelCalls)
  const usage = { inputTokens: 0, cachedTokens: 0, outputTokens: 0, toolsOffered: 0 }
  for (const call of calls) { usage.inputTokens += call.inputTokens; usage.cachedTokens += call.cachedTokens; usage.outputTokens += call.outputTokens; usage.toolsOffered += call.toolsOffered }
  const tools = turns.flatMap(t => t.toolCalls)
  return {
    arm: c.arm, id: k.id, kind: k.kind, status: first.status === 'canceled' ? 'canceled' : last.status, body: last.body,
    totalMs: turns.reduce((n, t) => n + (t.endedAt - t.startedAt), 0),
    firstActivityMs: first.firstActivityAt ? first.firstActivityAt - first.startedAt : undefined,
    firstTextMs: first.firstTextAt ? first.firstTextAt - first.startedAt : undefined,
    tools: tools.map(t => t.name), toolErrors: tools.filter(t => t.isError).length, elicitations,
    modelCalls: calls.filter(call => !call.error).length, failedModelCalls: calls.filter(call => call.error).length, ...usage, toolsOfferedMean: calls.length ? Math.round(usage.toolsOffered / calls.length) : 0,
    ...parallelStats(calls),
    stopLatencyMs: stopAt ? first.endedAt - stopAt : undefined,
    followUpStatus: followUp?.status, followUpBody: followUp?.body,
  }
}

async function runBaseline(c: Ctx, k: Case, log: (s: string) => void, sessions: string[]): Promise<Result> {
  const client = new FlowChatClient(baseConfig.flowUrl, cookie, 'dev-workspace')
  let elicitations = 0
  let stopAt = 0
  let stopped = false
  const onEvent = (e: { at: number; type: string; data: any }, turn: BaselineTurn) => {
    if (e.type !== 'text.delta' && e.type !== 'tool.delta' && e.type !== 'reasoning.delta') log(`+${((e.at - turn.startedAt) / 1000).toFixed(2)}s ${e.type} ${e.data.part?.toolCall?.name ?? ''} ${(e.data.error ?? '').slice(0, 200)}`)
    if (k.stopAfterFirstTool && !stopped && e.type === 'tool.started') {
      stopped = true
      setTimeout(() => { stopAt = Date.now(); log('   [host] stop signal'); void client.stop(turn.sessionId) }, 1000)
    }
  }
  const first = await client.turn(k.prompt(c), { onEvent })
  if (first.sessionId) sessions.push(first.sessionId)
  let turn = first
  const turns = [first]
  for (let i = 0; i < 3 && turn.pendingQuestion && !k.noAnswer; i++) {
    elicitations++
    const q = turn.pendingQuestion
    const value = q.approval ? 'approve' : (q.options.find(o => o.label.toLowerCase().includes((k.answer ?? '').toLowerCase())) ?? q.options[0])?.value ?? k.answer ?? 'ok'
    const label = q.approval ? 'Approve' : q.options.find(o => o.value === value)?.label ?? value
    log(`   [host] answer ${q.approval ? 'approval' : 'question'} -> ${label}`)
    turn = await client.turn(label, { sessionId: first.sessionId, answer: { partId: q.partId, value }, onEvent })
    turns.push(turn)
  }
  let followUp: BaselineTurn | undefined
  if (k.followUp) followUp = await client.turn(k.followUp, { sessionId: first.sessionId, onEvent })
  const status = stopAt && first.status !== 'response' ? 'canceled' : turn.pendingQuestion ? 'awaiting' : turn.status
  return {
    arm: c.arm, id: k.id, kind: k.kind, status, body: turn.body,
    totalMs: turns.reduce((n, t) => n + (t.endedAt - t.startedAt), 0),
    firstActivityMs: first.firstActivityAt ? first.firstActivityAt - first.startedAt : undefined,
    firstTextMs: first.firstTextAt ? first.firstTextAt - first.startedAt : undefined,
    tools: turns.flatMap(t => t.toolCalls.map(x => x.name)), toolErrors: turns.flatMap(t => t.toolCalls).filter(x => x.isError).length, elicitations,
    stopLatencyMs: stopAt ? first.endedAt - stopAt : undefined,
    followUpStatus: followUp?.status, followUpBody: followUp?.body,
  }
}

async function runArm(arm: Arm) {
  if (arm !== 'base') workers.set(arm, new FlowAgentWorker({ ...baseConfig, protocol: protocolOf[arm], baseURL: baseURLFor(protocolOf[arm]), stateDir: join(outDir, 'state') }))
  const c = await fixture(arm)
  const logFile = join(outDir, `${arm}${rep}.log`)
  const log = (s: string) => appendFileSync(logFile, `${s}\n`)
  log(`# arm ${arm} fixture ${c.issue}`)
  const results: Result[] = []
  const sessions: string[] = []
  for (const k of suite === 'all' ? [...cases, ...referenceCases] : cases) {
    if (only && !only.includes(k.id)) continue
    log(`\n## ${k.id} ${k.kind}\n> ${k.prompt(c)}`)
    let r: Result
    try {
      r = arm === 'base' ? await runBaseline(c, k, log, sessions) : await runWorker(c, k, log)
    } catch (error) {
      r = { arm, id: k.id, kind: k.kind, status: 'error', body: `harness: ${(error as Error).stack ?? error}`, totalMs: 0, tools: [], toolErrors: 0, elicitations: 0 }
    }
    try { r.check = await k.verify(c, r) } catch (error) { r.check = { pass: false, note: `verify: ${(error as Error).message}` } }
    log(`=> ${r.status} ${r.check.pass ? 'PASS' : 'FAIL'} ${r.check.note} | ${(r.totalMs / 1000).toFixed(1)}s tools=${r.tools.join(',')}\n${r.body}${r.followUpBody ? `\n[follow-up ${r.followUpStatus}] ${r.followUpBody}` : ''}`)
    console.log(`${arm.padEnd(4)} ${k.id.padEnd(3)} ${r.status.padEnd(8)} ${r.check.pass ? 'PASS' : 'FAIL'} ${(r.totalMs / 1000).toFixed(1).padStart(6)}s ${r.check.note}`)
    results.push(r)
    writeFileSync(join(outDir, `${arm}${rep}.json`), JSON.stringify({ fixture: c.issue, rep, sessions, results }, null, 2))
  }
  return results
}

const all = (await Promise.all(arms.map(runArm))).flat()
writeFileSync(join(outDir, 'results.json'), JSON.stringify(all, null, 2))
console.log('done', all.length)
