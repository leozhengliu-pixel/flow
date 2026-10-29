import type { BootstrapData, LoopContractOutput, LoopContractPlaceholder, LoopOutputContract, LoopOutputKind, LoopOutputOperation } from "@/types/flow";

type Translate = (source: string) => string;

/** Output kinds as the contract panel names them. */
export const CONTRACT_KIND_LABELS: Record<LoopOutputKind, string> = {
  document: "Document",
  issue: "Issue",
  comment: "Comment",
  statusUpdate: "Status update",
  change: "Other change",
};

/** Operations as the contract panel names them. */
export const CONTRACT_OPERATION_LABELS: Record<LoopOutputOperation, string> = {
  create: "Create",
  update: "Update",
  append: "Append",
  write: "Create or update",
  read: "Read only",
  forbidden: "Never",
};

const TARGET_MODE_LABELS: Record<string, string> = {
  new: "A new one each time",
  existing: "An existing one",
  triggering: "The triggering item",
  matching: "Issues matching a description",
  any: "Any",
};

export const CARDINALITY_LABELS: Record<string, string> = {
  onePerRun: "One per run",
  perItem: "One per item",
  any: "Any number",
};

/** The fallback contract's error the server stores (loopContractFallbackNotice in loop_output_contract.go). */
export const CONTRACT_FALLBACK_NOTICE = "Couldn't determine outputs automatically — set them";

/** Title placeholders the server fills (loopRuntimePlaceholders in loop_contract_targets.go). */
const RUNTIME_PLACEHOLDERS = ["date", "week", "month", "year", "day", "team", "project"];

/** Where an issue, comment or status update output goes, as the editor's "Target" select offers it. */
export type ContractTargetChoice = "any" | "triggering" | "matching" | "project" | "initiative";

export function operationsFor(kind: LoopOutputKind): LoopOutputOperation[] {
  return kind === "document" ? ["create", "update", "append", "write", "read", "forbidden"] : ["create", "update", "write", "read", "forbidden"];
}

export function writesOutput(operation: LoopOutputOperation) {
  return operation === "create" || operation === "update" || operation === "append" || operation === "write";
}

/** Whether an output makes new documents or issues (and so has a title template). */
export function createsOutput(output: Pick<LoopContractOutput, "kind" | "operation">) {
  return (output.kind === "document" || output.kind === "issue") && (output.operation === "create" || output.operation === "write");
}

/** A title template's placeholders the run fills: "Bug: {topic} {date}" gives ["topic"]. */
export function modelPlaceholders(template: string | undefined) {
  const names: string[] = [];
  for (const match of (template ?? "").matchAll(/\{([^{}]+)\}/g)) {
    const name = match[1].trim();
    if (name && !RUNTIME_PLACEHOLDERS.includes(name.toLowerCase()) && !names.includes(name)) names.push(name);
  }
  return names;
}

/** The placeholders of a template, keeping what fills each one that was already described. */
export function syncPlaceholders(template: string | undefined, current: LoopContractPlaceholder[] | undefined): LoopContractPlaceholder[] {
  return modelPlaceholders(template).map((name) => current?.find((item) => item.name.toLowerCase() === name.toLowerCase()) ?? { name });
}

/** An output that writes to an existing document the instructions do not name, and nobody picked yet. */
export function unchosenDocument(output: LoopContractOutput) {
  return output.kind === "document" && Boolean(output.target?.ambiguous) && !output.target?.documentId && (output.operation === "update" || output.operation === "append" || output.operation === "write");
}

/** The editor's target choice of an issue, comment or status update output. */
export function targetChoice(output: LoopContractOutput): ContractTargetChoice {
  const target = output.target ?? {};
  if (target.mode === "triggering") return "triggering";
  if (target.mode === "matching") return "matching";
  if (output.kind !== "issue" && (target.projectId || target.project)) return "project";
  if (output.kind !== "issue" && (target.initiativeId || target.initiative)) return "initiative";
  return "any";
}

/** The target choices an output offers: none for new issues (they take a team and project) and other changes. */
export function targetChoicesFor(output: Pick<LoopContractOutput, "kind" | "operation">): ContractTargetChoice[] {
  switch (output.kind) {
    case "comment":
      return ["any", "triggering", "matching", "project", "initiative"];
    case "statusUpdate":
      return ["any", "triggering", "project", "initiative"];
    case "issue":
      return output.operation === "create" ? [] : ["any", "triggering", "matching"];
  }
  return [];
}

/** The target an output gets for a choice: the fields that choice does not use are cleared. */
export function withTargetChoice(output: LoopContractOutput, choice: ContractTargetChoice): LoopContractOutput["target"] {
  const target = { ...output.target };
  const clear = { project: undefined, projectId: undefined, initiative: undefined, initiativeId: undefined, match: undefined };
  switch (choice) {
    case "triggering":
      return { ...target, ...clear, mode: "triggering" };
    case "matching":
      return { ...target, initiative: undefined, initiativeId: undefined, mode: "matching" };
    case "project":
      return { ...target, initiative: undefined, initiativeId: undefined, match: undefined, mode: "existing" };
    case "initiative":
      return { ...target, project: undefined, projectId: undefined, match: undefined, mode: "existing" };
  }
  return { ...target, ...clear, mode: output.kind === "issue" && output.operation === "create" ? "new" : "any" };
}

/** One line saying where an output goes: "titled 每日总结-{date} · in Engineering · one per run". */
export function contractTargetText(output: LoopContractOutput, data: Pick<BootstrapData, "teams" | "documents"> & Partial<Pick<BootstrapData, "projects" | "initiatives">>, t: Translate) {
  const parts: string[] = [];
  const target = output.target ?? {};
  const title = target.renderedTitle || target.titleTemplate;
  if (title && createsOutput(output)) parts.push(t("titled {title}").replace("{title}", () => title));
  const documentTitle = target.documentId ? (data.documents?.find((document) => document.id === target.documentId)?.title ?? target.title) : target.title;
  if (output.kind === "document" && documentTitle && output.operation !== "create") parts.push(t("the document {title}").replace("{title}", () => documentTitle));
  if (unchosenDocument(output)) parts.push(t("document not chosen yet"));
  if (target.mode === "matching") parts.push(t("issues matching {match}").replace("{match}", () => target.match || target.description || ""));
  const project = target.projectId ? (data.projects?.find((item) => item.id === target.projectId)?.name ?? target.project) : target.project;
  const initiative = target.initiativeId ? (data.initiatives?.find((item) => item.id === target.initiativeId)?.name ?? target.initiative) : target.initiative;
  const scoped = output.kind === "issue" || target.mode === "matching";
  if (project) parts.push((scoped ? t("in {project}") : t("on {project}")).replace("{project}", () => project));
  if (initiative && output.kind !== "issue") parts.push(t("on {initiative}").replace("{initiative}", () => initiative));
  if (!title && !documentTitle && !unchosenDocument(output) && !project && !initiative && target.mode && target.mode !== "any" && target.mode !== "matching") parts.push(t(TARGET_MODE_LABELS[target.mode] ?? target.mode));
  if (!documentTitle && target.description && !title && target.mode !== "matching") parts.push(target.description);
  const team = target.teamId ? (data.teams.find((item) => item.id === target.teamId)?.name ?? target.team) : target.team;
  if (team) parts.push(t("in {team}").replace("{team}", () => team));
  for (const placeholder of createsOutput(output) ? (target.placeholders ?? []) : []) {
    if (placeholder.source) parts.push(t("{name} filled by the run from {source}").replace("{name}", () => `{${placeholder.name}}`).replace("{source}", () => placeholder.source!));
  }
  if (output.cardinality && output.cardinality !== "any") parts.push(t(CARDINALITY_LABELS[output.cardinality]));
  if (output.conditions?.length) parts.push(t("when {condition}").replace("{condition}", () => output.conditions!.join("; ")));
  return parts.join(" · ");
}

export function blankOutput(): LoopContractOutput {
  return { kind: "document", operation: "create", required: true, target: { mode: "new" }, cardinality: "onePerRun" };
}

/** The contract a person saves: the edited outputs without what the server computes, every title placeholder declared. */
export function personContract(contract: LoopOutputContract): LoopOutputContract {
  return {
    source: "person",
    readOnly: contract.readOnly,
    readOnlyEvidence: contract.readOnlyEvidence,
    always: contract.always,
    alwaysEvidence: contract.alwaysEvidence,
    skipConditions: contract.skipConditions,
    outputs: (contract.readOnly ? contract.outputs.filter((output) => !writesOutput(output.operation)) : contract.outputs).map((output) => {
      const target = { ...output.target, renderedTitle: undefined };
      if (createsOutput(output)) target.placeholders = syncPlaceholders(target.titleTemplate, target.placeholders);
      return { ...output, target };
    }),
  };
}

/** Whether a contract being edited can be saved: every document output that writes to an existing one has it chosen. */
export function contractReady(contract: LoopOutputContract) {
  return contract.readOnly || !contract.outputs.some(unchosenDocument);
}
