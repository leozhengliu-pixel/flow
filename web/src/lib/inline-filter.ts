/**
 * Thin InlineFilter store (filter LS-0334) for ContentViewHeaderInlineSearch (LS-0142).
 * Holds the last search term consumed by list headers; full FilterBlock registry is out of scope.
 */
import { useCallback, useSyncExternalStore } from "react";
import { endsSession, registerSessionCacheClearer } from "@/lib/session-cache-registry";

type InlineFilterState = {
  lastSearchTerm: string;
};

const stores = new Map<string, InlineFilterState>();
const listeners = new Map<string, Set<() => void>>();

function ensure(key: string): InlineFilterState {
  let state = stores.get(key);
  if (!state) {
    state = { lastSearchTerm: "" };
    stores.set(key, state);
  }
  return state;
}

function emit(key: string) {
  for (const listener of listeners.get(key) ?? []) listener();
}

export function getInlineFilter(key: string) {
  return ensure(key);
}

export function setInlineFilterTerm(key: string, term: string) {
  const state = ensure(key);
  if (state.lastSearchTerm === term) return;
  state.lastSearchTerm = term;
  emit(key);
}

export function clearInlineFilter(key: string) {
  setInlineFilterTerm(key, "");
}

export function subscribeInlineFilter(key: string, listener: () => void) {
  let set = listeners.get(key);
  if (!set) {
    set = new Set();
    listeners.set(key, set);
  }
  set.add(listener);
  return () => { set!.delete(listener) };
}

export function useInlineFilter(key: string) {
  const state = useSyncExternalStore(
    (listener) => subscribeInlineFilter(key, listener),
    () => getInlineFilter(key),
    () => getInlineFilter(key),
  );
  const setTerm = useCallback(
    (term: string) => setInlineFilterTerm(key, term),
    [key],
  );
  return { lastSearchTerm: state.lastSearchTerm, setTerm };
}

// A search term typed by one viewer is not the next viewer's starting filter.
registerSessionCacheClearer((_viewerId, workspaceKey) => {
  if (!endsSession(workspaceKey)) return;
  for (const [key, state] of stores) {
    if (!state.lastSearchTerm) continue;
    state.lastSearchTerm = "";
    emit(key);
  }
  stores.clear();
});
