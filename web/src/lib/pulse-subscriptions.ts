import { useCallback, useEffect, useState } from "react";

import { fetchInitiativeProjectUpdatesSubscription, setInitiativeProjectUpdatesSubscription, setPulseSubscription } from "@/lib/api";
import { request } from "@/lib/api-client";
import type { Initiative, Project, PulseSubscriptionType, Subscription, TeamMember } from "@/types/flow";

/**
 * Pulse subscriptions (contract item 2). A project, initiative or team
 * subscription record carrying the `pulse` event is an explicit subscribe; an
 * explicit unsubscribe is stored in the record's `optOutEvents`. Without
 * an explicit choice Linear's default rules decide:
 * - project: member or lead, owner of one of its initiatives, or subscribed to
 *   "team project updates" of one of its teams (on by default for your teams);
 * - initiative: owner, or member of one of its projects (including the
 *   projects of its sub-initiatives);
 * - team: on for teams you belong to.
 */
export const PULSE_EVENT = "pulse";
export const PULSE_SUBSCRIPTIONS_CHANGED_EVENT = "flow:pulse-subscriptions-changed";
export type PulseSubscriptionChange = { type: PulseSubscriptionType; id: string; subscribed: boolean };

type SubscriptionRecord = Pick<Subscription, "userId" | "resourceType" | "resourceId" | "events"> & {
  /** Events the user explicitly unsubscribed from; `pulse` here overrides the default rules. */
  optOutEvents?: string[];
};

/** The viewer's explicit Pulse choice for one resource, or undefined when defaults apply. */
export function explicitPulseChoice(subscription: SubscriptionRecord | undefined): boolean | undefined {
  if (!subscription) return undefined;
  if (subscription.optOutEvents?.includes(PULSE_EVENT)) return false;
  if (subscription.events?.includes(PULSE_EVENT)) return true;
  return undefined;
}

function findSubscription(subscriptions: readonly SubscriptionRecord[] | undefined, viewerId: string, type: PulseSubscriptionType, id: string) {
  return subscriptions?.find(item => item.userId === viewerId && item.resourceType === type && item.resourceId === id);
}

export type PulseSubscriptionContext = {
  viewerId: string;
  subscriptions?: readonly SubscriptionRecord[];
  teamMembers?: readonly Pick<TeamMember, "teamId" | "userId">[];
  projects?: readonly Pick<Project, "id" | "lead" | "memberIds" | "initiatives">[];
  initiatives?: readonly Pick<Initiative, "id" | "owner" | "projectIds" | "parentInitiativeIds">[];
};

export function isProjectMember(project: Pick<Project, "lead" | "memberIds">, viewerId: string) {
  return project.lead?.id === viewerId || (project.memberIds ?? []).includes(viewerId);
}

export function teamPulseSubscribed(teamId: string, context: PulseSubscriptionContext): boolean {
  const explicit = explicitPulseChoice(findSubscription(context.subscriptions, context.viewerId, "team", teamId));
  if (explicit !== undefined) return explicit;
  return Boolean(context.teamMembers?.some(item => item.teamId === teamId && item.userId === context.viewerId));
}

export function projectPulseSubscribed(project: Pick<Project, "id" | "lead" | "memberIds" | "teamIds" | "initiatives">, context: PulseSubscriptionContext): boolean {
  const explicit = explicitPulseChoice(findSubscription(context.subscriptions, context.viewerId, "project", project.id));
  if (explicit !== undefined) return explicit;
  if (isProjectMember(project, context.viewerId)) return true;
  const initiativeIds = new Set(project.initiatives ?? []);
  if (context.initiatives?.some(item => item.owner?.id === context.viewerId && (initiativeIds.has(item.id) || item.projectIds?.includes(project.id)))) return true;
  return (project.teamIds ?? []).some(teamId => teamPulseSubscribed(teamId, context));
}

export function initiativePulseSubscribed(initiative: Pick<Initiative, "id" | "owner" | "projectIds">, context: PulseSubscriptionContext): boolean {
  const explicit = explicitPulseChoice(findSubscription(context.subscriptions, context.viewerId, "initiative", initiative.id));
  if (explicit !== undefined) return explicit;
  if (initiative.owner?.id === context.viewerId) return true;
  // Projects of this initiative and of its sub-initiatives, at any depth.
  const projectIds = new Set<string>(initiative.projectIds ?? []);
  const seen = new Set([initiative.id]);
  for (let frontier = [initiative.id]; frontier.length;) {
    const children = (context.initiatives ?? []).filter(item => !seen.has(item.id) && item.parentInitiativeIds?.some(parent => frontier.includes(parent)));
    children.forEach(child => { seen.add(child.id); child.projectIds?.forEach(id => projectIds.add(id)); });
    frontier = children.map(child => child.id);
  }
  for (const project of context.projects ?? []) {
    if ((projectIds.has(project.id) || project.initiatives?.some(id => seen.has(id))) && isProjectMember(project, context.viewerId)) return true;
  }
  return false;
}

/** The server's view (GET /api/pulse/subscriptions/{type}/{id}): explicit choice plus default rules. */
export function fetchPulseSubscriptionState(type: PulseSubscriptionType, id: string, signal?: AbortSignal): Promise<{ subscribed: boolean; explicit?: boolean }> {
  return request(`/api/pulse/subscriptions/${type}/${encodeURIComponent(id)}`, { signal });
}

/* Choices made in this session win over the (possibly stale) bootstrap data until it reloads. */
const sessionChoices = new Map<string, boolean>();
const choiceKey = (type: PulseSubscriptionType, id: string) => `${type}:${id}`;
export function sessionPulseChoice(type: PulseSubscriptionType, id: string) {
  return sessionChoices.get(choiceKey(type, id));
}
export function resetPulseSessionChoices() {
  sessionChoices.clear();
}

/** Subscribes or unsubscribes explicitly, then tells the Pulse feed and other menus. */
export async function changePulseSubscription(type: PulseSubscriptionType, id: string, subscribed: boolean): Promise<boolean> {
  const result = await setPulseSubscription(type, id, subscribed);
  const value = typeof result?.subscribed === "boolean" ? result.subscribed : subscribed;
  sessionChoices.set(choiceKey(type, id), value);
  window.dispatchEvent(new CustomEvent<PulseSubscriptionChange>(PULSE_SUBSCRIPTIONS_CHANGED_EVENT, { detail: { type, id, subscribed: value } }));
  return value;
}

/**
 * The viewer's Pulse subscription for one resource with an optimistic toggle.
 * `fallback` is the state derived from loaded data (explicit records + default
 * rules); while `refresh` is true (e.g. the menu is open) the server's answer
 * replaces it, since paged workspaces may not hold every membership locally.
 * `toggle` rolls back and rethrows on failure so the caller can show a toast.
 */
export function usePulseSubscription(type: PulseSubscriptionType, id: string, fallback: boolean, refresh = false) {
  const key = choiceKey(type, id);
  const [choice, setChoice] = useState<{ key: string; value: boolean | undefined }>(() => ({ key, value: sessionPulseChoice(type, id) }));
  const [server, setServer] = useState<{ key: string; value: boolean }>();
  const [saving, setSaving] = useState(false);
  const current = choice.key === key ? choice.value : sessionPulseChoice(type, id);
  useEffect(() => {
    const sync = (event: Event) => {
      const detail = (event as CustomEvent<PulseSubscriptionChange>).detail;
      if (detail && choiceKey(detail.type, detail.id) === key) setChoice({ key, value: detail.subscribed });
    };
    window.addEventListener(PULSE_SUBSCRIPTIONS_CHANGED_EVENT, sync);
    return () => window.removeEventListener(PULSE_SUBSCRIPTIONS_CHANGED_EVENT, sync);
  }, [key]);
  useEffect(() => {
    if (!refresh || !id) return;
    const controller = new AbortController();
    fetchPulseSubscriptionState(type, id, controller.signal)
      .then(state => { if (typeof state?.subscribed === "boolean") setServer({ key, value: state.subscribed }); })
      .catch(() => undefined);
    return () => controller.abort();
  }, [id, key, refresh, type]);
  const subscribed = current ?? (server?.key === key ? server.value : fallback);
  const toggle = useCallback(async (next: boolean) => {
    const previous = current;
    setChoice({ key, value: next });
    setSaving(true);
    try {
      const value = await changePulseSubscription(type, id, next);
      setChoice({ key, value });
      return value;
    } catch (error) {
      setChoice({ key, value: previous });
      throw error;
    } finally {
      setSaving(false);
    }
  }, [current, id, key, type]);
  return { subscribed, saving, toggle };
}

/* "Subscribe to initiative's project updates" (Linear's isSubscribedToSubProjects; off unless chosen). */
const projectUpdatesChoices = new Map<string, boolean>();
export const INITIATIVE_PROJECT_UPDATES_CHANGED_EVENT = "flow:initiative-project-updates-changed";

export async function changeInitiativeProjectUpdatesSubscription(initiativeId: string, subscribed: boolean): Promise<boolean> {
  const result = await setInitiativeProjectUpdatesSubscription(initiativeId, subscribed);
  const value = typeof result?.subscribed === "boolean" ? result.subscribed : subscribed;
  projectUpdatesChoices.set(initiativeId, value);
  window.dispatchEvent(new CustomEvent(INITIATIVE_PROJECT_UPDATES_CHANGED_EVENT, { detail: { id: initiativeId, subscribed: value } }));
  // The For me feed and the badge change with it.
  window.dispatchEvent(new CustomEvent<PulseSubscriptionChange>(PULSE_SUBSCRIPTIONS_CHANGED_EVENT, { detail: { type: "initiative", id: initiativeId, subscribed: value } }));
  return value;
}

/** The viewer's initiative project-updates subscription; the server's answer is read while `refresh` (menu open). */
export function useInitiativeProjectUpdatesSubscription(initiativeId: string | undefined, fallback = false, refresh = false) {
  const id = initiativeId ?? "";
  const [choice, setChoice] = useState<{ id: string; value: boolean | undefined }>(() => ({ id, value: projectUpdatesChoices.get(id) }));
  const [server, setServer] = useState<{ id: string; value: boolean }>();
  const current = choice.id === id ? choice.value : projectUpdatesChoices.get(id);
  useEffect(() => {
    const sync = (event: Event) => {
      const detail = (event as CustomEvent<{ id: string; subscribed: boolean }>).detail;
      if (detail?.id === id) setChoice({ id, value: detail.subscribed });
    };
    window.addEventListener(INITIATIVE_PROJECT_UPDATES_CHANGED_EVENT, sync);
    return () => window.removeEventListener(INITIATIVE_PROJECT_UPDATES_CHANGED_EVENT, sync);
  }, [id]);
  useEffect(() => {
    if (!refresh || !id) return;
    const controller = new AbortController();
    fetchInitiativeProjectUpdatesSubscription(id, controller.signal)
      .then(state => { if (typeof state?.subscribed === "boolean") setServer({ id, value: state.subscribed }); })
      .catch(() => undefined);
    return () => controller.abort();
  }, [id, refresh]);
  const subscribed = current ?? (server?.id === id ? server.value : fallback);
  const toggle = useCallback(async (next: boolean) => {
    const previous = current;
    setChoice({ id, value: next });
    try {
      const value = await changeInitiativeProjectUpdatesSubscription(id, next);
      setChoice({ id, value });
      return value;
    } catch (error) {
      setChoice({ id, value: previous });
      throw error;
    }
  }, [current, id]);
  return { subscribed, toggle };
}

/** Test hook. */
export function resetInitiativeProjectUpdatesChoices() {
  projectUpdatesChoices.clear();
}
