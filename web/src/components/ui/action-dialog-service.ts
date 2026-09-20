export type DateCompareOption = "on" | "before" | "after";

export type DateOrIntervalResult = {
  date: string;
  compare: DateCompareOption;
};

export type DiffPromptResult = "accept" | "reject" | "cancel";

export type DropdownPromptOption = {
  id: string;
  label: string;
  value: string;
  groupLabel?: string;
  disabled?: boolean;
};

export type DialogRequest =
  | {
      kind: "confirm";
      title: string;
      description?: string;
      confirmLabel: string;
      danger: boolean;
      resolve: (value: boolean) => void;
    }
  | {
      kind: "prompt";
      title: string;
      description?: string;
      defaultValue: string;
      confirmLabel: string;
      resolve: (value: string | null) => void;
    }
  | {
      kind: "date";
      title: string;
      description?: string;
      defaultValue: string;
      min?: string;
      max?: string;
      confirmLabel: string;
      resolve: (value: string | null) => void;
    }
  | {
      kind: "stringValidated";
      title: string;
      description?: string;
      defaultValue: string;
      placeholder?: string;
      pattern?: RegExp;
      patternMessage?: string;
      maxLength?: number;
      optional?: boolean;
      confirmLabel: string;
      resolve: (value: string | null) => void;
    }
  | {
      kind: "dateOrInterval";
      title: string;
      description?: string;
      defaultValue: string;
      initialCompare: DateCompareOption;
      confirmLabel: string;
      resolve: (value: DateOrIntervalResult | null) => void;
    }
  | {
      kind: "dropdown";
      title: string;
      description?: string;
      options: DropdownPromptOption[];
      selectedId?: string;
      disableOnDefault: boolean;
      confirmLabel: string;
      resolve: (value: string | null) => void;
    }
  | {
      kind: "diff";
      title: string;
      description?: string;
      diffs: string[];
      confirmLabel: string;
      rejectLabel?: string;
      cancelLabel?: string;
      danger: boolean;
      resolve: (value: DiffPromptResult) => void;
    };

const queue: DialogRequest[] = [];
const listeners = new Set<() => void>();
const emit = () => listeners.forEach(listener => listener());

export function confirmAction(title: string, options: { description?: string; confirmLabel?: string; danger?: boolean } = {}) {
  return new Promise<boolean>(resolve => {
    queue.push({
      kind: "confirm",
      title,
      description: options.description,
      confirmLabel: options.confirmLabel ?? "Confirm",
      danger: options.danger ?? true,
      resolve,
    });
    emit();
  });
}

export function promptAction(title: string, defaultValue = "", options: { description?: string; confirmLabel?: string } = {}) {
  return new Promise<string | null>(resolve => {
    queue.push({
      kind: "prompt",
      title,
      description: options.description,
      defaultValue,
      confirmLabel: options.confirmLabel ?? "Save",
      resolve,
    });
    emit();
  });
}

/** LS-0501 — Date confirmation (DateTimeControl). */
export function promptDateAction(
  title: string,
  defaultValue = "",
  options: { description?: string; confirmLabel?: string; min?: string; max?: string } = {},
) {
  return new Promise<string | null>(resolve => {
    queue.push({
      kind: "date",
      title,
      description: options.description,
      defaultValue: defaultValue || todayIso(),
      min: options.min,
      max: options.max,
      confirmLabel: options.confirmLabel ?? "Confirm",
      resolve,
    });
    emit();
  });
}

/** LS-0501 — Validated string (Required / Invalid value. / maxLength / regex). */
export function promptValidatedStringAction(
  title: string,
  defaultValue = "",
  options: {
    description?: string;
    confirmLabel?: string;
    placeholder?: string;
    pattern?: RegExp;
    patternMessage?: string;
    maxLength?: number;
    optional?: boolean;
  } = {},
) {
  return new Promise<string | null>(resolve => {
    queue.push({
      kind: "stringValidated",
      title,
      description: options.description,
      defaultValue,
      placeholder: options.placeholder,
      pattern: options.pattern,
      patternMessage: options.patternMessage,
      maxLength: options.maxLength,
      optional: options.optional ?? false,
      confirmLabel: options.confirmLabel ?? "Confirm",
      resolve,
    });
    emit();
  });
}

/** LS-0501 — Date or interval with on / before / after compare tabs. */
export function promptDateOrIntervalAction(
  title: string,
  defaultValue = "",
  options: { description?: string; confirmLabel?: string; initialCompare?: DateCompareOption } = {},
) {
  return new Promise<DateOrIntervalResult | null>(resolve => {
    queue.push({
      kind: "dateOrInterval",
      title,
      description: options.description,
      defaultValue: defaultValue || todayIso(),
      initialCompare: options.initialCompare ?? "after",
      confirmLabel: options.confirmLabel ?? "Confirm",
      resolve,
    });
    emit();
  });
}

/** LS-0501 — Dropdown confirmation. */
export function promptDropdownAction(
  title: string,
  dropdownOptions: DropdownPromptOption[],
  options: { description?: string; confirmLabel?: string; selectedId?: string; disableOnDefault?: boolean } = {},
) {
  return new Promise<string | null>(resolve => {
    queue.push({
      kind: "dropdown",
      title,
      description: options.description,
      options: dropdownOptions,
      selectedId: options.selectedId ?? dropdownOptions[0]?.id,
      disableOnDefault: options.disableOnDefault ?? false,
      confirmLabel: options.confirmLabel ?? "Confirm",
      resolve,
    });
    emit();
  });
}

/** LS-0501 — Diff confirmation (accept / reject / cancel). */
export function promptDiffAction(
  title: string,
  diffs: string[],
  options: {
    description?: string;
    confirmLabel?: string;
    rejectLabel?: string;
    cancelLabel?: string;
    danger?: boolean;
  } = {},
) {
  return new Promise<DiffPromptResult>(resolve => {
    queue.push({
      kind: "diff",
      title,
      description: options.description,
      diffs,
      confirmLabel: options.confirmLabel ?? "Accept",
      rejectLabel: options.rejectLabel,
      cancelLabel: options.cancelLabel ?? "Cancel",
      danger: options.danger ?? false,
      resolve,
    });
    emit();
  });
}

export const currentActionDialog = () => queue[0];
export const subscribeActionDialogs = (listener: () => void) => {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
};

export function completeActionDialog(result: boolean | string | DateOrIntervalResult | DiffPromptResult | null) {
  const current = queue.shift();
  if (!current) return;
  switch (current.kind) {
    case "confirm":
      current.resolve(Boolean(result));
      break;
    case "prompt":
    case "date":
    case "stringValidated":
    case "dropdown":
      current.resolve(typeof result === "string" ? result : null);
      break;
    case "dateOrInterval":
      current.resolve(result && typeof result === "object" && "date" in result ? result : null);
      break;
    case "diff":
      current.resolve(result === "accept" || result === "reject" ? result : "cancel");
      break;
  }
  queueMicrotask(emit);
}

export function validatePromptString(
  value: string,
  options: { pattern?: RegExp; patternMessage?: string; maxLength?: number; optional?: boolean },
): string | undefined {
  const trimmed = value.trim();
  if (!trimmed) {
    if (options.optional) return undefined;
    return "Required";
  }
  if (options.maxLength != null && trimmed.length > options.maxLength) {
    return options.patternMessage ?? "Invalid value.";
  }
  if (options.pattern && !options.pattern.test(trimmed)) {
    return options.patternMessage ?? "Invalid value.";
  }
  return undefined;
}

function todayIso() {
  const now = new Date();
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}-${String(now.getDate()).padStart(2, "0")}`;
}
