import { readFile } from 'node:fs/promises'
import type { McpServer, RegisterTool } from '../server.js'
import { registerAskUser } from './ask-user.js'
import { registerBrowserTools } from './browser.js'
import { proposeEnvironmentTool, type ProposeEnvironmentArgs } from './propose-environment.js'

// Registers every Flow MCP tool. Each tool lives in its own file in this
// directory; add one line per tool file below. env is the MCP server's
// environment (FLOW_RUNTIME_URL, FLOW_TOKEN_FILE, FLOW_TURN_ID, ...), which
// the runner sets in each harness's MCP config.
export function registerBuiltinTools(server: McpServer, env: NodeJS.ProcessEnv = process.env): void {
  const registerTool: RegisterTool = server.registerTool.bind(server)
  registerAskUser(server)
  // coding sessions (track I): browser_* and attach_screenshot, only when the
  // bootstrap enabled browser testing (FLOW_BROWSER=1).
  registerBrowserTools((name, schema, handler) => registerTool(name, schema, (args) => handler(args)), {
    enabled: server.context.browserEnabled,
    env,
  })
  // coding sessions (track K): propose_environment ("Configure/Fix with agent").
  const runtimeUrl = env.FLOW_RUNTIME_URL
  const tokenFile = env.FLOW_TOKEN_FILE
  if (runtimeUrl && tokenFile) {
    registerTool(
      proposeEnvironmentTool.name,
      { description: proposeEnvironmentTool.description, inputSchema: proposeEnvironmentTool.inputSchema },
      (args) =>
        proposeEnvironmentTool.handler(args as unknown as ProposeEnvironmentArgs, {
          runtimeUrl,
          token: async () => (await readFile(tokenFile, 'utf8')).trim(),
        }),
    )
  }
}
