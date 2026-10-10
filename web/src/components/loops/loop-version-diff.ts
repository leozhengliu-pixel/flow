import type { Loop, LoopVersion, LoopVersionChange } from "@/types/flow";

type Translate = (source: string) => string;

export const CHANGE_LABELS: Record<Exclude<LoopVersionChange, "published" | "restored">, string> = {
  name: "Name",
  trigger: "Trigger",
  instructions: "Instructions",
  connectors: "Connectors",
  teamAccess: "Team access",
  allowChangesOutsideTrigger: "Changes outside trigger",
  allowExternalSync: "Externally synced changes",
  webSearch: "Web search",
  codeAccess: "Access code",
  level: "Location",
};

/** "Published", "Restored version 2", "Changed trigger, instructions". */
export function versionSummary(version: Pick<LoopVersion, "changeSummary" | "restoredFromVersion">, t: Translate = (value) => value) {
  const changes = version.changeSummary ?? [];
  if (changes.includes("restored")) return t("Restored version {version}").replace("{version}", String(version.restoredFromVersion ?? "?"));
  if (changes.includes("published") || !changes.length) return t("Published");
  const fields = changes.filter((item): item is keyof typeof CHANGE_LABELS => item in CHANGE_LABELS).map((item) => t(CHANGE_LABELS[item]).toLocaleLowerCase());
  return t("Changed {fields}").replace("{fields}", fields.join(", "));
}

const stable = (value: unknown) => JSON.stringify(value ?? null, (_key, item) => (item && typeof item === "object" && !Array.isArray(item) ? Object.fromEntries(Object.entries(item).sort(([a], [b]) => a.localeCompare(b))) : item));

/** Definition fields where a version differs from the loop as it is now (same groups as `changeSummary`). */
export function versionDiff(definition: LoopVersion["definition"], loop: Loop): (keyof typeof CHANGE_LABELS)[] {
  const changed: (keyof typeof CHANGE_LABELS)[] = [];
  if ((definition.name ?? "") !== (loop.name ?? "")) changed.push("name");
  if (definition.triggerType !== loop.triggerType || stable(definition.triggerConfig) !== stable(loop.triggerConfig)) changed.push("trigger");
  if ((definition.instructions ?? "").trim() !== (loop.instructions ?? "").trim()) changed.push("instructions");
  if (stable([...(definition.connectorIds ?? [])].sort()) !== stable([...(loop.connectorIds ?? [])].sort())) changed.push("connectors");
  if (definition.teamAccess !== loop.teamAccess) changed.push("teamAccess");
  if (Boolean(definition.allowChangesOutsideTrigger) !== Boolean(loop.allowChangesOutsideTrigger)) changed.push("allowChangesOutsideTrigger");
  if (Boolean(definition.allowExternalSync) !== Boolean(loop.allowExternalSync)) changed.push("allowExternalSync");
  if (Boolean(definition.webSearch) !== Boolean(loop.webSearch)) changed.push("webSearch");
  if ((definition.codeAccess ?? "read") !== (loop.codeAccess ?? "read")) changed.push("codeAccess");
  if (definition.level !== loop.level || (definition.level === "team" && definition.teamId !== loop.teamId)) changed.push("level");
  return changed;
}
