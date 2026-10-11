import { describe, expect, it } from 'vitest'
import type { TemplateFormField } from '@/types/flow'
import { missingRequiredFields, templateFormDescription } from './template-form'

const fields: TemplateFormField[] = [
  { id: 'a', type: 'text', label: 'Steps', required: true },
  { id: 'b', type: 'checkboxes', label: 'Browsers', required: true, options: ['Chrome', 'Safari'] },
  { id: 'c', type: 'instructions', label: 'Note', description: 'Add screenshots below.', required: false },
  { id: 'd', type: 'priority', label: 'Priority', required: true },
  { id: 'e', type: 'title', label: 'Title', required: false },
]

describe('form template answers', () => {
  it('lists the required fields left empty, reading property fields from the composer', () => {
    expect(missingRequiredFields(fields, {}, { title: '', priority: 0, dueDate: '' })).toEqual(['a', 'b', 'd'])
    expect(missingRequiredFields(fields, { a: 'x', b: ['Chrome'] }, { title: '', priority: 2, dueDate: '' })).toEqual([])
  })

  it('writes answered fields as bold labels, keeps instructions and skips property fields', () => {
    expect(templateFormDescription(fields, { a: ' Click save ', b: ['Chrome', 'Safari'] })).toBe('**Steps**\nClick save\n\n**Browsers**\n- Chrome\n- Safari\n\nAdd screenshots below.')
  })
})
