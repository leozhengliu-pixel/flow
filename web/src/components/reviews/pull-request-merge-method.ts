export type MergeMethod = "squash" | "merge" | "rebase";

export function resolvePreferredMergeMethod(
  preference: string | undefined,
): MergeMethod {
  const value = (preference ?? "").toLowerCase();
  if (value.includes("rebase")) return "rebase";
  if (value.includes("merge commit") || value === "merge") return "merge";
  return "squash";
}
