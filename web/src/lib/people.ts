import type { User } from '@/types/flow'

export type PersonIdentity = Pick<User, 'id'> & Partial<Omit<User, 'id'>> & { label?: string }

export function personSearchText(person: PersonIdentity) {
  return [person.id, person.userId, person.name, person.displayName, person.label, person.email].filter(Boolean).join(' ')
}

export function personIdentifier(person: PersonIdentity) { return person.userId || person.id }

export function personMatchesQuery(person: PersonIdentity, query: string) {
  const needle = query.trim().toLocaleLowerCase()
  if (!needle) return true
  const fields = [person.id, person.userId, person.name, person.displayName, person.label, person.email].filter((field): field is string => Boolean(field))
  if (fields.some(field => field.toLocaleLowerCase().includes(needle))) return true
  const compact = (value: string) => value.toLocaleLowerCase().replace(/[\s._-]+/g, '')
  const compactNeedle = compact(needle)
  if (compactNeedle && [person.id, person.userId].some(id => id && compact(id).includes(compactNeedle))) return true
  // Fuzzy-match names individually. Never assemble an employee number across fields.
  if (/\d/.test(needle)) return false
  return [person.name, person.displayName, person.label].some(name => {
    if (!name) return false
    let offset = 0
    for (const character of needle) {
      offset = name.toLocaleLowerCase().indexOf(character, offset)
      if (offset < 0) return false
      offset++
    }
    return true
  })
}

export function isPeopleProperty(property: string) {
  return /^(assignees?|default assignee|assigned to|assign|assign to|project lead|projectLead|lead|owner|members?|users?|subscribers?|creator|delegate|负责人|默认负责人|项目负责人|所有者|成员|用户|订阅者|创建者|代理人)$/i.test(property.trim().replace(/[… .]+$/, ''))
}

export function directoryPerson(users: ReadonlyMap<string, User>, id: string) {
  return users.get(id) ?? users.get(id.replace(/^(assignee|creator|owner|lead|project-lead|member|subscriber|delegate):/, ''))
}
