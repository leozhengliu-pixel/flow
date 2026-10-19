/**
 * propose_environment MCP tool (docs/coding-sessions-design.md §16, §18). Owner: track K.
 *
 * "Configure with agent" / "Fix with agent" sessions call it once the proposed
 * setup works in the sandbox. The proposal is posted to
 * POST {runtime}/internal/coding-runtime/environment-proposal and stored by
 * Flow as the environment's pendingProposal, which an admin applies or
 * discards. Proposals never carry secret values: Flow strips variables that
 * look like secrets and lists them in missingSecrets.
 *
 * Self-contained on purpose: track C's MCP server registers it with
 * registerTool(proposeEnvironmentTool.name, proposeEnvironmentTool.inputSchema,
 * (args) => proposeEnvironmentTool.handler(args, context)).
 */

export interface ProposeEnvironmentContext {
  /** Flow runtime base URL (FLOW_RUNTIME_URL). */
  runtimeUrl: string;
  /** Returns the sandbox token (read from FLOW_TOKEN_FILE). */
  token: () => Promise<string> | string;
  fetch?: typeof fetch;
}

export interface ProposeEnvironmentArgs {
  tools?: Record<string, string>;
  files?: { path: string; content: string }[];
  environmentVariables?: { name: string; value: string }[];
  prepareScript?: string;
  customGuidance?: string;
  dependencyManagement?: "custom-only" | "custom-and-generated";
  sandboxSize?: "small" | "medium" | "large";
  missingSecrets?: string[];
  explanation: string;
  verified: boolean;
}

export interface ToolResult {
  content: { type: "text"; text: string }[];
  isError?: boolean;
}

export const proposeEnvironmentTool = {
  name: "propose_environment",
  description:
    "Propose the coding environment setup for this repository (mise tools, files, plain environment variables, prepare script). " +
    "Call it after the setup ran successfully in this sandbox. Never include secret values; list required secret names in missingSecrets.",
  inputSchema: {
    type: "object",
    additionalProperties: false,
    required: ["explanation", "verified"],
    properties: {
      tools: { type: "object", additionalProperties: { type: "string" }, description: "mise tool → version, e.g. {\"node\":\"22\"}" },
      files: {
        type: "array",
        items: { type: "object", required: ["path", "content"], properties: { path: { type: "string" }, content: { type: "string" } } },
        description: "Files written relative to the home directory",
      },
      environmentVariables: {
        type: "array",
        items: { type: "object", required: ["name", "value"], properties: { name: { type: "string" }, value: { type: "string" } } },
        description: "Plain, non-secret variables",
      },
      prepareScript: { type: "string", description: "Shell commands that install dependencies" },
      customGuidance: { type: "string" },
      dependencyManagement: { type: "string", enum: ["custom-only", "custom-and-generated"] },
      sandboxSize: { type: "string", enum: ["small", "medium", "large"] },
      missingSecrets: { type: "array", items: { type: "string" }, description: "Names of secrets the user must add" },
      explanation: { type: "string", description: "What the setup does and why" },
      verified: { type: "boolean", description: "True only if the setup succeeded in this sandbox" },
    },
  },

  async handler(args: ProposeEnvironmentArgs, context: ProposeEnvironmentContext): Promise<ToolResult> {
    const hasMise = args.tools !== undefined || args.files !== undefined || args.environmentVariables !== undefined;
    const body = {
      mise: hasMise ? { tools: args.tools, files: args.files, environmentVariables: args.environmentVariables } : undefined,
      prepareScript: args.prepareScript,
      customGuidance: args.customGuidance,
      dependencyManagement: args.dependencyManagement,
      sandboxSize: args.sandboxSize,
      missingSecrets: args.missingSecrets,
      explanation: args.explanation ?? "",
      verified: Boolean(args.verified),
    };
    const doFetch = context.fetch ?? fetch;
    const url = `${context.runtimeUrl.replace(/\/+$/, "")}/internal/coding-runtime/environment-proposal`;
    let lastError = "";
    for (let attempt = 0; attempt < 3; attempt++) {
      try {
        const response = await doFetch(url, {
          method: "POST",
          headers: { "Content-Type": "application/json", Authorization: `Bearer ${await context.token()}` },
          body: JSON.stringify(body),
        });
        const text = await response.text();
        if (response.ok) {
          const result = text ? (JSON.parse(text) as { missingSecrets?: string[] }) : {};
          const missing = result.missingSecrets?.length ? ` The user must add these secrets: ${result.missingSecrets.join(", ")}.` : "";
          return { content: [{ type: "text", text: `Proposal saved for review.${missing}` }] };
        }
        lastError = `${response.status}: ${text.slice(0, 500)}`;
        if (response.status < 500) break; // validation errors do not improve with retries
      } catch (error) {
        lastError = error instanceof Error ? error.message : String(error);
      }
      await new Promise((resolve) => setTimeout(resolve, 1000 * 2 ** attempt));
    }
    return { content: [{ type: "text", text: `Flow rejected the proposal (${lastError}). Fix it and call propose_environment again.` }], isError: true };
  },
};
