/** Answers to a form issue template's fields, and how they become the new issue's description. */
import type { TemplateFormField } from '@/types/flow'

export type TemplateFormValues = Record<string, string | string[]>

/** The fields left empty that the template marks required (property fields read the composer's own state). */
export function missingRequiredFields(fields: TemplateFormField[], values: TemplateFormValues, state: { title: string; priority: number; dueDate: string }) {
  return fields.filter(field => {
    if (!field.required || field.type === 'instructions' || field.type === 'upload' || field.type === 'labelGroup') return false
    if (field.type === 'title') return !state.title.trim()
    if (field.type === 'priority') return state.priority === 0
    if (field.type === 'dueDate') return !state.dueDate
    const value = values[field.id]
    return Array.isArray(value) ? value.length === 0 : !(value ?? '').trim()
  }).map(field => field.id)
}

/** Markdown description built from the answers: each answered field as a bold label over its value. */
export function templateFormDescription(fields: TemplateFormField[], values: TemplateFormValues) {
  const blocks: string[] = []
  for (const field of fields) {
    if (field.type === 'instructions') { const text = (field.description || field.label).trim(); if (text) blocks.push(text); continue }
    if (['title', 'priority', 'dueDate', 'upload', 'labelGroup'].includes(field.type)) continue
    const value = values[field.id]
    const answer = Array.isArray(value) ? value.map(item => `- ${item}`).join('\n') : (value ?? '').trim()
    if (!answer) continue
    blocks.push(`**${field.label}**\n${answer}`)
  }
  return blocks.join('\n\n')
}
