import type { TeamSettings } from "@/types/flow";

export type EstimateType = TeamSettings["estimateType"];

type EstimateScaleSettings = Partial<
  Pick<TeamSettings, "estimateType" | "estimateAllowZero" | "estimateExtended">
>;

const BASE_VALUES: Record<Exclude<EstimateType, "notUsed">, number[]> = {
  exponential: [1, 2, 4, 8, 16],
  fibonacci: [1, 2, 3, 5, 8],
  flow: [1, 2, 3, 4, 5],
  tShirt: [1, 2, 3, 5, 8],
};

const EXTENDED_VALUES: Record<Exclude<EstimateType, "notUsed">, number[]> = {
  exponential: [32, 64],
  fibonacci: [13, 21],
  flow: [6, 7],
  tShirt: [13, 21],
};

const T_SHIRT_LABELS: Record<number, string> = {
  0: "-",
  1: "XS",
  2: "S",
  3: "M",
  5: "L",
  8: "XL",
  13: "XXL",
  21: "XXXL",
};

export const ESTIMATE_TYPE_NAMES: Record<EstimateType, string> = {
  notUsed: "Not in use",
  exponential: "Exponential",
  fibonacci: "Fibonacci",
  flow: "Linear",
  tShirt: "T-Shirt",
};

/** Point values a team can pick, honoring zero and extended-scale options. */
export function estimateValues(settings: EstimateScaleSettings | undefined): number[] {
  const type = settings?.estimateType ?? "notUsed";
  if (type === "notUsed") return [];
  return [
    ...(settings?.estimateAllowZero ? [0] : []),
    ...BASE_VALUES[type],
    ...(settings?.estimateExtended ? EXTENDED_VALUES[type] : []),
  ];
}

/** "(0, 1, 2, 4, 8, 16 Points)"-style summary shown next to a scale name. */
export function estimateScaleDetails(type: EstimateType, allowZero = false, extended = false): string {
  if (type === "notUsed") return "";
  const values = estimateValues({ estimateType: type, estimateAllowZero: allowZero, estimateExtended: extended });
  if (type === "tShirt") return `(${values.map((value) => T_SHIRT_LABELS[value]).join(", ")})`;
  return `(${values.join(", ")} Points)`;
}

export function estimateLabel(value: number | undefined, type: EstimateType): string {
  if (value === undefined || value === null) return "Estimate";
  if (type === "tShirt") return T_SHIRT_LABELS[value] ?? `${value} Points`;
  return `${value} Point${value === 1 ? "" : "s"}`;
}

/** Short value shown on chips and the composer pill: the number, or the T-shirt size. */
export function estimateShortLabel(value: number, type: EstimateType): string {
  if (type === "tShirt") return T_SHIRT_LABELS[value] ?? String(value);
  return String(value);
}

/** Points an unestimated issue contributes to totals (1 unless turned off). */
export function unestimatedValue(settings: Pick<TeamSettings, "estimateCountUnestimated"> | undefined): number {
  return settings?.estimateCountUnestimated === false ? 0 : 1;
}

type EstimatePointSettings = Partial<Pick<TeamSettings, "estimateType" | "estimateAllowZero" | "estimateCountUnestimated">>;

export function teamUsesEstimates(settings: Pick<TeamSettings, "estimateType"> | Partial<Pick<TeamSettings, "estimateType">> | undefined): boolean {
  return Boolean(settings?.estimateType && settings.estimateType !== "notUsed");
}

/**
 * Points an issue contributes to sums, scope and capacity. Unestimated issues
 * count as 1 point unless "Count unestimated issues" is off; a 0 estimate only
 * counts as 0 when the team allows zero estimates (otherwise it is treated as
 * unestimated, matching the server's cycle graph).
 */
export function estimatePoints(estimate: number | null | undefined, settings: EstimatePointSettings | undefined): number {
  if (estimate === undefined || estimate === null || estimate < 0 || (estimate === 0 && !settings?.estimateAllowZero)) return unestimatedValue(settings);
  return estimate;
}

/** Sum of estimate points of the issues whose team uses estimates; undefined when none does. */
export function sumEstimatePoints<T extends { estimate?: number | null; teamId?: string }>(
  issues: readonly T[],
  settingsForTeam: (teamId: string | undefined) => EstimatePointSettings | undefined,
): number | undefined {
  let total: number | undefined;
  for (const issue of issues) {
    const settings = settingsForTeam(issue.teamId);
    if (!teamUsesEstimates(settings)) continue;
    total = (total ?? 0) + estimatePoints(issue.estimate, settings);
  }
  return total;
}

/** Point totals as shown next to the estimate icon ("8", "2.5"). */
export function formatEstimatePoints(points: number): string {
  return Number.isInteger(points) ? String(points) : String(Math.round(points * 10) / 10);
}

/** Picker value for "No estimate"; the API treats -1 as clearing the estimate. */
export const NO_ESTIMATE = -1;

/**
 * The estimate an issue of a team gets when several issues are set to `value`
 * at once: undefined (left alone) when the team does not use estimates,
 * NO_ESTIMATE to clear it, the value itself when it is on the team's scale,
 * otherwise the nearest value of the scale (the larger one on a tie).
 */
export function estimateOnTeamScale(value: number, settings: (EstimateScaleSettings & Partial<Pick<TeamSettings, "estimateType">>) | undefined): number | undefined {
  if (!teamUsesEstimates(settings)) return undefined;
  if (value < 0) return NO_ESTIMATE;
  const scale = estimateValues(settings);
  if (scale.includes(value)) return value;
  return scale.reduce((best, item) => Math.abs(item - value) < Math.abs(best - value) || (Math.abs(item - value) === Math.abs(best - value) && item > best) ? item : best, scale[0]);
}

/**
 * Bulk estimate choices for the scales of the selected teams that use
 * estimates: one scale as it is, several merged (T-shirt labels only when every
 * scale is T-shirt). Empty when no selected team uses estimates.
 */
export function estimateBulkValues(scales: readonly (EstimateScaleSettings | undefined)[]): { values: number[]; type: EstimateType; settings: EstimateScaleSettings | undefined } {
  const used = scales.filter((settings) => teamUsesEstimates(settings));
  if (!used.length) return { values: [], type: "notUsed", settings: undefined };
  const first = used[0];
  const same = used.every((settings) => settings?.estimateType === first?.estimateType && estimateValues(settings).join() === estimateValues(first).join());
  if (same) return { values: estimateValues(first), type: first?.estimateType ?? "notUsed", settings: first };
  const values = [...new Set(used.flatMap((settings) => estimateValues(settings)))].sort((a, b) => a - b);
  return { values, type: used.every((settings) => settings?.estimateType === "tShirt") ? "tShirt" : "fibonacci", settings: undefined };
}

export function estimatePickerOptions(settings: EstimateScaleSettings | undefined) {
  const type = settings?.estimateType ?? "notUsed";
  return [
    { id: String(NO_ESTIMATE), label: "No estimate", value: NO_ESTIMATE },
    ...estimateValues(settings).map((value) => ({ id: String(value), label: estimateLabel(value, type), value })),
  ];
}
