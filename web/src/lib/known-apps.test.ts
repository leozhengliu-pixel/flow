import { describe, expect, it } from 'vitest'
import { APP_LOGO_MARKS } from '@/assets/app-logos/logos'
import { KNOWN_APPS, matchKnownApp, type KnownAppClient } from './known-apps'

const cases: [string, KnownAppClient, string | undefined][] = [
  // Names: case-, space- and punctuation-insensitive, leading words only.
  ['plain name', { name: 'Claude' }, 'claude'],
  ['name case and spaces', { name: '  claude   DESKTOP ' }, 'claude'],
  ['Claude Code beats Claude', { name: 'Claude Code' }, 'claude-code'],
  ['Claude Code DCR name with server suffix', { name: 'Claude Code (flow-app)' }, 'claude-code'],
  ['kebab case', { name: 'claude-code' }, 'claude-code'],
  ['compact camel case', { name: 'ClaudeCode' }, 'claude-code'],
  ['Codex rmcp client', { name: 'codex-mcp-client' }, 'codex'],
  ['OpenAI Codex is Codex, not ChatGPT', { name: 'OpenAI Codex' }, 'codex'],
  ['ChatGPT', { name: 'ChatGPT' }, 'chatgpt'],
  ['VS Code', { name: 'Visual Studio Code' }, 'vscode'],
  ['VS Code Insiders', { name: 'Visual Studio Code - Insiders' }, 'vscode'],
  ['vscode compact', { name: 'VSCode' }, 'vscode'],
  ['opencode lower case', { name: 'opencode' }, 'opencode'],
  ['goose', { name: 'goose' }, 'goose'],
  ['Gemini CLI', { name: 'Gemini CLI MCP Client' }, 'gemini'],
  ['Zed', { name: 'Zed' }, 'zed'],
  ['Windsurf', { name: 'Windsurf' }, 'windsurf'],
  ['Raycast', { name: 'Raycast' }, 'raycast'],
  ['Cline', { name: 'Cline' }, 'cline'],
  ['GitHub Copilot', { name: 'GitHub Copilot' }, 'github-copilot'],
  // Not a leading word, or a different word.
  ['brand inside another word', { name: 'Claudette' }, undefined],
  ['brand not leading', { name: 'My Claude integration' }, undefined],
  ['bare Copilot is ambiguous', { name: 'Copilot' }, undefined],
  ['unknown app', { name: 'Deploy bot' }, undefined],
  ['generic MCP proxy', { name: 'mcp-remote' }, undefined],
  ['empty', { name: '' }, undefined],
  ['punctuation only', { name: '()' }, undefined],
  ['nothing', {}, undefined],
  // Redirect URIs: app-owned hosts and schemes identify the app whatever the name says.
  ['claude.ai callback', { name: 'mcp-remote', redirectUris: ['https://claude.ai/api/mcp/auth_callback'] }, 'claude'],
  ['claude.com callback', { redirectUris: ['https://claude.com/api/mcp/auth_callback'] }, 'claude'],
  ['ChatGPT connector callback', { name: 'connector', redirectUris: ['https://chatgpt.com/connector_platform_oauth_redirect'] }, 'chatgpt'],
  ['cursor scheme', { name: 'Untitled', redirectUris: ['cursor://anysphere.cursor-retrieval/oauth/user-abc/callback'] }, 'cursor'],
  ['upper-case scheme', { redirectUris: ['CURSOR://anysphere.cursor-mcp/oauth/callback'] }, 'cursor'],
  ['vscode.dev', { redirectUris: ['https://insiders.vscode.dev/redirect'] }, 'vscode'],
  ['VS Code built-in extension', { redirectUris: ['vscode://vscode.github-authentication/did-authenticate'] }, 'vscode'],
  ['Cline extension in VS Code', { name: 'Cline', redirectUris: ['vscode://saoudrizwan.claude-dev/mcp-auth/callback'] }, 'cline'],
  ['Cline extension in Cursor', { redirectUris: ['cursor://saoudrizwan.claude-dev/mcp-auth/callback'] }, 'cline'],
  ['unknown VS Code extension', { redirectUris: ['vscode://someone.other-ext/callback'] }, undefined],
  ['windsurf scheme', { redirectUris: ['windsurf://codeium.windsurf/mcp/callback'] }, 'windsurf'],
  ['lookalike host', { name: 'x', redirectUris: ['https://claude.ai.evil.test/callback'] }, undefined],
  ['host suffix without dot', { redirectUris: ['https://notclaude.ai/callback'] }, undefined],
  // A name never borrows a brand for a client that sends its codes elsewhere.
  ['loopback keeps name match', { name: 'Claude Code (flow)', redirectUris: ['http://localhost:54545/callback'] }, 'claude-code'],
  ['127.0.0.1 keeps name match', { name: 'opencode', redirectUris: ['http://127.0.0.1:19876/mcp/oauth/callback'] }, 'opencode'],
  ['IPv6 loopback keeps name match', { name: 'Codex', redirectUris: ['http://[::1]:1455/callback'] }, 'codex'],
  ['foreign web host rejects name', { name: 'Claude', redirectUris: ['https://evil.example.test/callback'] }, undefined],
  ['foreign scheme rejects name', { name: 'Cursor', redirectUris: ['evilapp://callback'] }, undefined],
  ['one foreign redirect among loopback', { name: 'Claude Code', redirectUris: ['http://localhost:1/cb', 'https://evil.example.test/cb'] }, undefined],
  ['own host keeps name match', { name: 'Claude', redirectUris: ['https://claude.ai/api/mcp/auth_callback'] }, 'claude'],
  ['invalid redirect rejects name', { name: 'Claude', redirectUris: ['not a url'] }, undefined],
  ['blank redirects are ignored', { name: 'Zed', redirectUris: ['', '  ', null] }, 'zed'],
]

describe('matchKnownApp', () => {
  it.each(cases)('%s', (_, client, expected) => {
    expect(matchKnownApp(client)?.id).toBe(expected)
  })

  it('resolves every app to a vendored logo and keeps ids and aliases unique', () => {
    const ids = KNOWN_APPS.map(app => app.id)
    expect(new Set(ids).size).toBe(ids.length)
    const aliases = KNOWN_APPS.flatMap(app => app.aliases)
    expect(new Set(aliases).size).toBe(aliases.length)
    for (const app of KNOWN_APPS) {
      expect(APP_LOGO_MARKS[app.logo].path.length).toBeGreaterThan(10)
      expect(matchKnownApp({ name: app.name })?.id).toBe(app.id)
    }
  })

  it('returns the mark with its viewBox and brand colour (or none for currentColor marks)', () => {
    expect(matchKnownApp({ name: 'Claude' })?.mark).toMatchObject({ viewBox: '0 0 24 24', color: '#D97757' })
    expect(matchKnownApp({ name: 'Cursor' })?.mark.color).toBeUndefined()
  })
})
