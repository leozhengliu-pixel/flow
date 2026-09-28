/**
 * Lightweight selected-models store for command relevance (LS-0721).
 * Explorer / detail surfaces write model type names; command menu reads them.
 */
import { useSyncExternalStore } from "react";

let selectedModels = new Set<string>();
const listeners = new Set<() => void>();

function emit() {
  for (const listener of listeners) listener();
}

export function setSelectedModels(models: Iterable<string>) {
  const next = new Set([...models].filter(Boolean));
  let changed =
    next.size !== selectedModels.size ||
    [...next].some((model) => !selectedModels.has(model));
  if (!changed) return;
  selectedModels = next;
  emit();
}

export function clearSelectedModels() {
  if (selectedModels.size === 0) return;
  selectedModels = new Set();
  emit();
}

export function getSelectedModels() {
  return selectedModels;
}

export function subscribeSelectedModels(listener: () => void) {
  listeners.add(listener);
  return () => { listeners.delete(listener) };
}

export function useSelectedModels() {
  return useSyncExternalStore(
    subscribeSelectedModels,
    getSelectedModels,
    () => selectedModels,
  );
}
