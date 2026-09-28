/**
 * LS-0721 useActionGroupsForSelection — selectedModels → allowedActionGroups bridge.
 */
import { useEffect, useMemo, useState } from "react";
import {
  clearSelectedModels,
  useSelectedModels,
} from "@/lib/selected-models-store";

let allowedActionGroups: string[] = [];
const listeners = new Set<() => void>();

function emitAllowed() {
  for (const listener of listeners) listener();
}

export function getAllowedActionGroups() {
  return allowedActionGroups;
}

export function subscribeAllowedActionGroups(listener: () => void) {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}

function setAllowedActionGroups(next: string[]) {
  const same =
    next.length === allowedActionGroups.length &&
    next.every((value, index) => value === allowedActionGroups[index]);
  if (same) return;
  allowedActionGroups = next;
  emitAllowed();
}

/**
 * When the selection store has models, publish `groups` as the allow-list.
 * Clears the allow-list when selection is empty or the hook unmounts.
 */
export function useActionGroupsForSelection(
  groups: string | string[],
): string[] {
  const selected = useSelectedModels();
  const groupsKey = Array.isArray(groups) ? groups.join("\0") : groups;
  const normalized = useMemo(
    () => (Array.isArray(groups) ? groups : [groups]).filter(Boolean),
    // groupsKey captures identity of the allow-list values
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [groupsKey],
  );

  useEffect(() => {
    if (selected.size > 0) setAllowedActionGroups(normalized);
    else setAllowedActionGroups([]);
    return () => setAllowedActionGroups([]);
  }, [normalized, selected]);

  return selected.size > 0 ? normalized : [];
}

/** Subscribe to the published allow-list (command menu). */
export function useAllowedActionGroups() {
  const [groups, setGroups] = useState(getAllowedActionGroups);
  useEffect(() => subscribeAllowedActionGroups(() => { setGroups([...getAllowedActionGroups()]); }), []);
  return groups;
}

export { clearSelectedModels };
