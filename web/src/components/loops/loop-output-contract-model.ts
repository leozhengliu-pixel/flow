import type { BootstrapData, LoopContractOutput, LoopOutputContract, LoopOutputKind, LoopOutputOperation } from "@/types/flow";

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
  any: "Any",
};

export const CARDINALITY_LABELS: Record<string, string> = {
  onePerRun: "One per run",
  perItem: "One per item",
  any: "Any number",
};

/** The fallback contract's error the server stores (loopContractFallbackNotice in loop_output_contract.go). */
export const CONTRACT_FALLBACK_NOTICE = "Couldn't determine outputs automatically — set them";

export function operationsFor(kind: LoopOutputKind): LoopOutputOperation[] {
  return kind === "document" ? ["create", "update", "append", "write", "read", "forbidden"] : ["create", "update", "write", "read", "forbidden"];
}

export function writesOutput(operation: LoopOutputOperation) {
  return operation === "create" || operation === "update" || operation === "append" || operation === "write";
}

/** One line saying where an output goes: "titled 每日总结-{date} · in Engineering · one per run". */
export function contractTargetText(output: LoopContractOutput, data: Pick<BootstrapData, "teams" | "documents">, t: Translate) {
  const parts: string[] = [];
  const target = output.target ?? {};
  const title = target.renderedTitle || target.titleTemplate;
  if (title && (output.operation === "create" || output.operation === "write")) parts.push(t("titled {title}").replace("{title}", () => title));
  const documentTitle = target.documentId ? (data.documents?.find((document) => document.id === target.documentId)?.title ?? target.title) : target.title;
  if (documentTitle && output.operation !== "create") parts.push(t("the document {title}").replace("{title}", () => documentTitle));
  if (!title && !documentTitle && target.mode && target.mode !== "any") parts.push(t(TARGET_MODE_LABELS[target.mode] ?? target.mode));
  if (!documentTitle && target.description && !title) parts.push(target.description);
  const team = target.teamId ? (data.teams.find((item) => item.id === target.teamId)?.name ?? target.team) : target.team;
  if (team) parts.push(t("in {team}").replace("{team}", () => team));
  if (output.cardinality && output.cardinality !== "any") parts.push(t(CARDINALITY_LABELS[output.cardinality]));
  if (output.conditions?.length) parts.push(t("when {condition}").replace("{condition}", () => output.conditions!.join("; ")));
  return parts.join(" · ");
}

export function blankOutput(): LoopContractOutput {
  return { kind: "document", operation: "create", required: true, target: { mode: "new" }, cardinality: "onePerRun" };
}

/** The contract a person saves: the edited outputs without what the server computes. */
export function personContract(contract: LoopOutputContract): LoopOutputContract {
  return {
    source: "person",
    readOnly: contract.readOnly,
    readOnlyEvidence: contract.readOnlyEvidence,
    always: contract.always,
    alwaysEvidence: contract.alwaysEvidence,
    skipConditions: contract.skipConditions,
    outputs: (contract.readOnly ? contract.outputs.filter((output) => !writesOutput(output.operation)) : contract.outputs).map((output) => ({ ...output, target: { ...output.target, renderedTitle: undefined } })),
  };
}
