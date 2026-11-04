import { describe, expect, it } from 'vitest'
import type { BootstrapData, User } from '@/types/flow'
import { userMentionPattern } from './agent-answer-content'

function person(id: string, displayName: string, extra: Partial<User> = {}) {
  return { id, displayName, name: displayName, username: '', active: true, app: false, ...extra } as User
}

const data = {
  users: [
    person('u-wang', '王伟'),
    person('u-li', '李'),
    person('u-anna', 'Anna Li'),
    person('u-b', 'b'),
    person('u-rocket', '🚀 Rocket Team'),
    person('u-app', 'Bot', { app: true }),
    person('u-gone', 'Gone', { active: false }),
  ],
} as unknown as BootstrapData

function mentioned(text: string) {
  const mentions = userMentionPattern(data)!
  return [...text.matchAll(mentions.pattern)].map(match => mentions.byName.get(match[2].toLowerCase())!.id)
}

describe('userMentionPattern', () => {
  it.each([
    ['hello @Anna Li, please look', ['u-anna']],
    ['(@王伟)', ['u-wang']],
    ['@王伟', ['u-wang']],
    ['请@王伟看一下', ['u-wang']],
    ['，@Anna Li 你好', ['u-anna']],
    ['你好：@王伟', ['u-wang']],
    ['（@李）', ['u-li']],
    ['請@李 看看', ['u-li']],
    ['@李', ['u-li']],
    ['cc @b', ['u-b']],
    ['谢谢@🚀 Rocket Team', ['u-rocket']],
    ['@王伟 和 @李', ['u-wang', 'u-li']],
  ])('mentions people in %s', (text, expected) => {
    expect(mentioned(text)).toEqual(expected)
  })

  it.each([
    ['mail a@b.com now'],
    ['mail anna@b.com now'],
    ['a@Anna Li'],
    ['ab@王伟'],
    ['王@b.com'],
    ['@李白 wrote it'],
    ['@李4'],
    ['@nobody'],
    ['@Bot and @Gone'],
  ])('leaves %s as plain text', text => {
    expect(mentioned(text)).toEqual([])
  })

  it('has no pattern without any mentionable people', () => {
    expect(userMentionPattern({ users: [person('a', 'A', { active: false })] } as unknown as BootstrapData)).toBeUndefined()
  })
})
