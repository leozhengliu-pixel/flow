/**
 * LS-0031 AgentMentionBodyData — mention / skill-preset body helper for compose-with-agent.
 * Builds ProseMirror-shaped doc JSON (entityMention attrs) used by Loops / automation compose.
 */

export type AgentMentionAttrs = {
  id: string
  label: string
  href?: string
}

export type AgentMentionBodyDoc = {
  type: 'doc'
  content: Array<{
    type: 'paragraph'
    content: Array<
      | { type: 'entityMention'; attrs: AgentMentionAttrs }
      | { type: 'text'; text: string }
    >
  }>
}

const BUILTIN_SKILL_PRESETS: Record<string, { title: string }> = {
  triage: { title: 'Triage' },
  summarize: { title: 'Summarize' },
  'write-update': { title: 'Write update' },
  'draft-reply': { title: 'Draft reply' },
}

export class AgentMentionBodyData {
  static build(attrs: AgentMentionAttrs): AgentMentionBodyDoc {
    return {
      type: 'doc',
      content: [
        {
          type: 'paragraph',
          content: [
            { type: 'entityMention', attrs: { href: '', ...attrs } },
            { type: 'text', text: ' ' },
          ],
        },
      ],
    }
  }

  static buildPreset(presetId: string, options?: { label?: string }): AgentMentionBodyDoc {
    const builtin = BUILTIN_SKILL_PRESETS[presetId]
    return AgentMentionBodyData.build({
      id: `skill-preset:${presetId}`,
      label: options?.label ?? builtin?.title ?? presetId,
      href: '',
    })
  }

  static buildSkill(skill: { id: string; name: string }): AgentMentionBodyDoc {
    return AgentMentionBodyData.build({
      id: skill.id,
      label: skill.name,
      href: '',
    })
  }

  /** Flatten mention body into plain text suitable for REST agent compose prompts. */
  static toPlainText(doc: AgentMentionBodyDoc, trailing = ''): string {
    const parts: string[] = []
    for (const block of doc.content) {
      for (const node of block.content) {
        if (node.type === 'entityMention') parts.push(`@${node.attrs.label}`)
        else if (node.type === 'text') parts.push(node.text)
      }
    }
    const base = parts.join('').trim()
    return trailing ? `${base} ${trailing}`.trim() : base
  }

  static isSkillPresetId(id: string): boolean {
    return id.startsWith('skill-preset:')
  }

  static presetIdFromMention(id: string): string | undefined {
    return AgentMentionBodyData.isSkillPresetId(id) ? id.slice('skill-preset:'.length) : undefined
  }
}
