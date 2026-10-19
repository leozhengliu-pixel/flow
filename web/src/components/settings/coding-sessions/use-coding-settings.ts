import { useCallback, useEffect, useRef, useState } from "react";
import {
  fetchCodingEnvironments,
  fetchCodingModels,
  fetchCodingSettings,
  fetchCodingStatus,
  updateCodingSettings,
} from "@/lib/api";
import { ApiError } from "@/lib/api-client";
import { toast } from "@/lib/toast";
import type { CodingAgentSettings, CodingEnvironment, CodingModelOption, CodingSettings, CodingStatus } from "@/types/flow";
import { legacyCodingSettings, normalizeCodingSettings } from "./coding-sessions-model";

export function errorMessage(error: unknown, fallback: string): string {
  return error instanceof Error && error.message ? error.message : fallback;
}

/** Server status (code hosts, regions, sizes). `undefined` while loading or when the API is unavailable. */
export function useCodingStatus() {
  const [status, setStatus] = useState<CodingStatus>();
  const [loaded, setLoaded] = useState(false);
  useEffect(() => {
    let active = true;
    fetchCodingStatus()
      .then((next) => active && setStatus(next))
      .catch(() => undefined)
      .finally(() => active && setLoaded(true));
    return () => {
      active = false;
    };
  }, []);
  return { status, loaded };
}

export function useCodingModels() {
  const [models, setModels] = useState<CodingModelOption[]>([]);
  useEffect(() => {
    let active = true;
    fetchCodingModels()
      .then((next) => active && setModels(Array.isArray(next) ? next : []))
      .catch(() => undefined);
    return () => {
      active = false;
    };
  }, []);
  return models;
}

/** Workspace policy (`/api/coding/settings`) with optimistic, serialized PATCHes. */
export function useCodingPolicy(legacy: CodingAgentSettings | undefined, enabled: boolean) {
  const [settings, setSettings] = useState<CodingSettings>(() => legacyCodingSettings(legacy, enabled));
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string>();
  const current = useRef(settings);
  current.current = settings;
  useEffect(() => {
    let active = true;
    fetchCodingSettings()
      .then((next) => active && setSettings(normalizeCodingSettings(next)))
      .catch(() => undefined);
    return () => {
      active = false;
    };
  }, []);
  const update = useCallback(async (patch: Partial<CodingSettings>) => {
    const before = current.current;
    setSettings({ ...before, ...patch });
    setSaving(true);
    try {
      const saved = await updateCodingSettings(patch);
      setSettings(saved ? normalizeCodingSettings(saved) : { ...before, ...patch });
      setError(undefined);
    } catch (reason) {
      setSettings(before);
      // Server-side validation (400) is shown inline next to the controls; other failures toast.
      if (reason instanceof ApiError && reason.status === 400) setError(errorMessage(reason, "Invalid coding session settings"));
      else toast.error(errorMessage(reason, "Could not update coding session settings"));
    } finally {
      setSaving(false);
    }
  }, []);
  return { settings, update, saving, error };
}

export function useCodingEnvironments(includeArchived: boolean) {
  const [environments, setEnvironments] = useState<CodingEnvironment[]>([]);
  const [archived, setArchived] = useState<CodingEnvironment[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState<string>();
  const reload = useCallback(async () => {
    try {
      const [active, old] = await Promise.all([
        fetchCodingEnvironments(false),
        includeArchived ? fetchCodingEnvironments(true) : Promise.resolve([] as CodingEnvironment[]),
      ]);
      setEnvironments((active ?? []).filter((environment) => !environment.archivedAt));
      setArchived((old ?? []).filter((environment) => environment.archivedAt));
      setError(undefined);
    } catch (reason) {
      setError(errorMessage(reason, "Could not load environments"));
    } finally {
      setLoaded(true);
    }
  }, [includeArchived]);
  useEffect(() => {
    void reload();
  }, [reload]);
  return { environments, archived, loaded, error, reload };
}
