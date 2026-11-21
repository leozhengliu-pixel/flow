import { useCallback, useEffect, useRef, useState } from "react";
import { listIssueRecords } from "@/lib/api";
import type { Issue } from "@/types/flow";
import { LoaderResultCache } from "@/lib/loader-result-cache";
import { ApiError } from "@/lib/api-client";
import { loadLocalArchivePage, type LocalSliceScope } from "@/lib/local-store-slices";

export type ArchivedModelsLoaderOptions = {
  teamId: string;
  modelsPerLoad?: number;
  loadThreshold?: number;
  autoLoadOnMount?: boolean;
  workspaceKey?: string;
  localScope?: LocalSliceScope;
};

export type ArchivedModelsLoaderResult = {
  loading: boolean;
  models: Issue[];
  totalCount?: number;
  hasMore: boolean;
  error?: string;
  loadMore: (distanceFromEnd?: number) => void;
  reload: () => void;
};

type CachePayload = {
  models: Issue[];
  cursor?: string;
  hasMore: boolean;
  totalCount?: number;
};

const issueCache = new LoaderResultCache<CachePayload>();

/**
 * LS-0725 — archived issues incremental loader via REST `listIssueRecords`
 * (`archived:'true'` + cursor). Default page size 50, loadThreshold ≈ 30.
 */
export function useArchivedModelsLoader(
  options: ArchivedModelsLoaderOptions,
): ArchivedModelsLoaderResult {
  const {
    teamId,
    modelsPerLoad = 50,
    loadThreshold = 30,
    autoLoadOnMount = true,
    workspaceKey,
    localScope,
  } = options;
  const viewerId = localScope?.viewerId;
  const workspaceId = localScope?.workspaceId;
  const cacheKey = `issue:${workspaceKey ?? ''}:${viewerId ?? ''}:${teamId}:${modelsPerLoad}`;
  const [loadedKey, setLoadedKey] = useState('');
  const [models, setModels] = useState<Issue[]>([]);
  const [loading, setLoading] = useState(false);
  const [hasMore, setHasMore] = useState(true);
  const [totalCount, setTotalCount] = useState<number | undefined>();
  const [error, setError] = useState<string | undefined>();
  const cursorRef = useRef<string | undefined>(undefined);
  const hasMoreRef = useRef(true);
  const inFlight = useRef<AbortController | undefined>(undefined);

  const fetchPage = useCallback(
    async (reset: boolean, refresh = false) => {
      if (inFlight.current && !refresh) return;
      if (!reset && !hasMoreRef.current) return;
      inFlight.current?.abort();
      const controller = new AbortController();
      const { signal } = controller;
      inFlight.current = controller;
      const scope = workspaceId && workspaceKey && viewerId ? { workspaceId, workspaceKey, viewerId } : undefined;
      setLoading(true);
      setError(undefined);
      try {
        if (reset) {
          const cached = scope ? undefined : issueCache.get(cacheKey);
          if (cached) {
            setLoadedKey(cacheKey);
            setModels(cached.models);
            cursorRef.current = cached.cursor;
            hasMoreRef.current = cached.hasMore;
            setHasMore(cached.hasMore);
            setTotalCount(cached.totalCount);
          } else {
            setModels([]);
            cursorRef.current = undefined;
            hasMoreRef.current = true;
            setHasMore(true);
            setTotalCount(undefined);
          }
        }
        const cursor = reset ? undefined : cursorRef.current;
        const page = scope ? await loadLocalArchivePage(teamId, modelsPerLoad, cursor, scope, signal, refresh) : await listIssueRecords(
          {
            teamId,
            archived: "true",
            limit: modelsPerLoad,
            cursor,
            includeTotal: true,
            sort: "updatedAt",
            direction: "desc",
          },
          signal,
          workspaceKey,
        );
        if (signal.aborted) return;
        const nextHasMore = Boolean(page.hasMore && page.nextCursor);
        cursorRef.current = page.nextCursor;
        hasMoreRef.current = nextHasMore;
        setHasMore(nextHasMore);
        setTotalCount(page.total);
        setLoadedKey(cacheKey);
        setModels((current) => {
          const merged = reset ? page.items : [...current, ...page.items];
          const seen = new Set<string>();
          const deduped = merged.filter((item) => {
            if (seen.has(item.id)) return false;
            seen.add(item.id);
            return true;
          });
          if (!scope) issueCache.set(cacheKey, {
            models: deduped,
            cursor: page.nextCursor,
            hasMore: nextHasMore,
            totalCount: page.total,
          });
          return deduped;
        });
      } catch (cause) {
        if (signal.aborted) return;
        if (cause instanceof ApiError && [401, 403, 404].includes(cause.status)) setLoadedKey('');
        setError(
          cause instanceof Error
            ? cause.message
            : "Could not load archived issues",
        );
      } finally {
        if (inFlight.current === controller) {
          inFlight.current = undefined;
          setLoading(false);
        }
      }
    },
    [cacheKey, modelsPerLoad, teamId, viewerId, workspaceId, workspaceKey],
  );

  useEffect(() => {
    if (!autoLoadOnMount || !teamId) return;
    // The load flips several state slots up front; start it from a microtask so the effect itself never sets state.
    let active = true;
    queueMicrotask(() => { if (active) void fetchPage(true); });
    return () => {
      active = false;
      inFlight.current?.abort();
      inFlight.current = undefined;
    };
  }, [teamId, autoLoadOnMount, fetchPage]);

  const loadMore = useCallback(
    (distanceFromEnd?: number) => {
      if (distanceFromEnd !== undefined && distanceFromEnd > loadThreshold)
        return;
      if (!hasMoreRef.current || inFlight.current) return;
      void fetchPage(false);
    },
    [fetchPage, loadThreshold],
  );

  const reload = useCallback(() => {
    issueCache.expire(cacheKey);
    void fetchPage(true, true);
  }, [cacheKey, fetchPage]);

  return {
    loading,
    models: loadedKey === cacheKey ? models : [],
    totalCount: loadedKey === cacheKey ? totalCount : undefined,
    hasMore,
    error,
    loadMore,
    reload,
  };
}
