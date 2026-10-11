/**
 * The fields of a form issue template, filled in the new-issue composer instead of a free description (the reference app's
 * "Custom Form" templates). Property fields write the issue's own properties (title, priority, due date); the other
 * answers are collected into the description, in field order, with the instructions text kept verbatim.
 */
import { useI18n } from '@/i18n/i18n'
import type { TemplateFormField } from '@/types/flow'
import type { TemplateFormValues } from '@/lib/template-form'
import './template-form-fields.css'


const PRIORITY_NAMES = ['No priority', 'Urgent', 'High', 'Medium', 'Low']

export interface TemplateFormFillProps {
  fields: TemplateFormField[]
  values: TemplateFormValues
  onChange: (values: TemplateFormValues) => void
  title: string
  onTitle: (title: string) => void
  priority: number
  onPriority: (priority: number) => void
  dueDate: string
  onDueDate: (date: string) => void
  /** Field ids that failed "required" validation on the last submit. */
  invalid?: Set<string>
}

export function TemplateFormFill({ fields, values, onChange, title, onTitle, priority, onPriority, dueDate, onDueDate, invalid }: TemplateFormFillProps) {
  const { t } = useI18n()
  const set = (id: string, value: string | string[]) => onChange({ ...values, [id]: value })
  return <div className="template-form-fill" data-i18n-ignore>
    {fields.map(field => {
      if (field.type === 'upload' || field.type === 'labelGroup') return null
      const value = values[field.id]
      const text = typeof value === 'string' ? value : ''
      const id = `template-field-${field.id}`
      const label = field.label || t(field.type === 'title' ? 'Title' : field.type === 'priority' ? 'Priority' : field.type === 'dueDate' ? 'Due date' : 'Untitled field')
      if (field.type === 'instructions') return <p className="template-form-instructions" key={field.id}>{field.description || field.label}</p>
      return <div className={`template-form-field${invalid?.has(field.id) ? ' is-invalid' : ''}`} key={field.id}>
        <label htmlFor={id}>{label}{field.required && <span aria-hidden="true"> *</span>}</label>
        {field.description && <span className="template-form-field-description">{field.description}</span>}
        {field.type === 'text' && <input id={id} value={text} placeholder={t('Enter text')} onChange={event => set(field.id, event.target.value)}/>}
        {field.type === 'title' && <input id={id} value={title} placeholder={t('Enter text')} onChange={event => onTitle(event.target.value)}/>}
        {field.type === 'longText' && <textarea id={id} rows={3} value={text} placeholder={t('Enter text')} onChange={event => set(field.id, event.target.value)}/>}
        {field.type === 'date' && <input id={id} type="date" value={text} onChange={event => set(field.id, event.target.value)}/>}
        {field.type === 'dueDate' && <input id={id} type="date" value={dueDate} onChange={event => onDueDate(event.target.value)}/>}
        {field.type === 'dropdown' && <select id={id} value={text} onChange={event => set(field.id, event.target.value)}>
          <option value="">{t('Select an option')}</option>
          {(field.options ?? []).filter(Boolean).map(option => <option key={option} value={option}>{option}</option>)}
        </select>}
        {field.type === 'priority' && <select id={id} value={String(priority)} onChange={event => onPriority(Number(event.target.value))}>
          {PRIORITY_NAMES.map((name, index) => <option key={name} value={index}>{t(name)}</option>)}
        </select>}
        {field.type === 'checkboxes' && <div className="template-form-checkboxes" role="group" aria-labelledby={id}>
          {(field.options ?? []).filter(Boolean).map(option => {
            const checked = Array.isArray(value) && value.includes(option)
            return <label key={option}><input type="checkbox" checked={checked} onChange={() => set(field.id, checked ? (value as string[]).filter(item => item !== option) : [...(Array.isArray(value) ? value : []), option])}/>{option}</label>
          })}
        </div>}
      </div>
    })}
  </div>
}
