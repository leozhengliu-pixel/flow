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

/** Points an unestimated issue contributes to totals (1 unless turned off). */
export function unestimatedValue(settings: Pick<TeamSettings, "estimateCountUnestimated"> | undefined): number {
  return settings?.estimateCountUnestimated === false ? 0 : 1;
}

/** Picker value for "No estimate"; the API treats -1 as clearing the estimate. */
export const NO_ESTIMATE = -1;

export function estimatePickerOptions(settings: EstimateScaleSettings | undefined) {
  const type = settings?.estimateType ?? "notUsed";
  return [
    { id: String(NO_ESTIMATE), label: "No estimate", value: NO_ESTIMATE },
    ...estimateValues(settings).map((value) => ({ id: String(value), label: estimateLabel(value, type), value })),
  ];
}
