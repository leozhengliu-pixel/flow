/**
 * Streams ask_user's question while the model writes it (like api/cmd/server agent_stream.go since 9d62921):
 * the question is the tool call's arguments, so the worker reads the `question` string out of the partial JSON
 * on every argument delta and emits a running question under the call id the finished elicitation reuses.
 *
 * Raw provider events differ per protocol:
 *  - Responses: response.output_item.added (function_call: id, call_id, name) + response.function_call_arguments.delta
 *  - Chat Completions: choices[0].delta.tool_calls[{ index, id?, function: { name?, arguments } }]
 *  - AI SDK (Anthropic): tool-input-start { id, toolName } + tool-input-delta { id, delta }
 */
export class QuestionStreamer {
  private calls = new Map<string, { callId: string; name: string; args: string; last: string }>()

  constructor(private readonly tool = 'ask_user') {}

  /** Feeds one raw model event; returns the question text so far when it changed. */
  feed(raw: any): { callId: string; text: string } | undefined {
    if (!raw || typeof raw !== 'object') return undefined
    if (raw.type === 'response.output_item.added' && raw.item?.type === 'function_call') {
      this.calls.set(raw.item.id, { callId: raw.item.call_id, name: raw.item.name, args: '', last: '' })
      return undefined
    }
    if (raw.type === 'response.function_call_arguments.delta') return this.append(raw.item_id, raw.delta)
    if (raw.type === 'tool-input-start') {
      this.calls.set(raw.id, { callId: raw.id, name: raw.toolName, args: '', last: '' })
      return undefined
    }
    if (raw.type === 'tool-input-delta') return this.append(raw.id, raw.delta)
    const toolCalls = raw.choices?.[0]?.delta?.tool_calls
    if (Array.isArray(toolCalls)) {
      let out: { callId: string; text: string } | undefined
      for (const call of toolCalls) {
        const key = `chat:${call.index ?? 0}`
        if (call.id || call.function?.name) {
          const existing = this.calls.get(key)
          this.calls.set(key, { callId: call.id ?? existing?.callId ?? key, name: call.function?.name ?? existing?.name ?? '', args: existing?.args ?? '', last: existing?.last ?? '' })
        }
        if (call.function?.arguments) out = this.append(key, call.function.arguments) ?? out
      }
      return out
    }
    return undefined
  }

  private append(key: string, delta: unknown) {
    const call = this.calls.get(key)
    if (!call || typeof delta !== 'string') return undefined
    call.args += delta
    if (call.name !== this.tool) return undefined
    const text = partialJSONStringField(call.args, 'question')
    if (!text || text === call.last) return undefined
    call.last = text
    return { callId: call.callId, text }
  }
}

/** The string value of a top-level key in JSON that is still streaming in (port of the Go helper). */
export function partialJSONStringField(raw: string, key: string): string {
  const needle = `"${key}"`
  let start = -1
  for (let offset = 0; offset < raw.length;) {
    const at = raw.indexOf(needle, offset)
    if (at < 0) return ''
    if (at === 0 || raw[at - 1] !== '\\') {
      start = at + needle.length
      break
    }
    offset = at + 1
  }
  if (start < 0) return ''
  let index = start
  const skip = () => { while (index < raw.length && ' \n\t\r'.includes(raw[index])) index++ }
  skip()
  if (raw[index] !== ':') return ''
  index++
  skip()
  if (raw[index] !== '"') return ''
  index++
  let out = ''
  const simple: Record<string, string> = { n: '\n', t: '\t', r: '\r', b: '\b', f: '\f' }
  while (index < raw.length) {
    const char = raw[index]
    if (char === '"') return out
    if (char !== '\\') { out += char; index++; continue }
    if (index + 1 >= raw.length) return out
    const escaped = raw[index + 1]
    if (escaped === 'u') {
      if (index + 6 > raw.length) return out
      const code = Number.parseInt(raw.slice(index + 2, index + 6), 16)
      if (Number.isNaN(code)) return out
      index += 6
      if (code >= 0xd800 && code <= 0xdbff) {
        if (index + 6 > raw.length || raw[index] !== '\\' || raw[index + 1] !== 'u') return out
        const low = Number.parseInt(raw.slice(index + 2, index + 6), 16)
        if (Number.isNaN(low)) return out
        out += String.fromCharCode(code, low)
        index += 6
        continue
      }
      out += String.fromCharCode(code)
      continue
    }
    out += simple[escaped] ?? escaped
    index += 2
  }
  return out
}
