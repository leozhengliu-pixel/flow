export type DialogRequest =
  | { kind: "confirm"; title: string; description?: string; confirmLabel: string; danger: boolean; resolve: (value: boolean) => void }
  | { kind: "prompt"; title: string; description?: string; defaultValue: string; confirmLabel: string; resolve: (value: string | null) => void };

const queue: DialogRequest[] = [];
const listeners = new Set<() => void>();
const emit = () => listeners.forEach(listener => listener());

export function confirmAction(title: string, options: { description?: string; confirmLabel?: string; danger?: boolean } = {}) {
  return new Promise<boolean>(resolve => {
    queue.push({ kind: "confirm", title, description: options.description, confirmLabel: options.confirmLabel ?? "Confirm", danger: options.danger ?? true, resolve });
    emit();
  });
}

export function promptAction(title: string, defaultValue = "", options: { description?: string; confirmLabel?: string } = {}) {
  return new Promise<string | null>(resolve => {
    queue.push({ kind: "prompt", title, description: options.description, defaultValue, confirmLabel: options.confirmLabel ?? "Save", resolve });
    emit();
  });
}

export const currentActionDialog = () => queue[0];
export const subscribeActionDialogs = (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; };
export function completeActionDialog(result: boolean | string | null) {
  const current = queue.shift();
  if (current?.kind === "confirm") current.resolve(Boolean(result));
  if (current?.kind === "prompt") current.resolve(typeof result === "string" ? result : null);
  queueMicrotask(emit);
}
