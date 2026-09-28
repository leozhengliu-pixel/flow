import { useCallback, useEffect, useRef, useState } from "react";
import { listIssueRecords } from "@/lib/api";
import type { Issue } from "@/types/flow";
import { LoaderResultCache } from "@/lib/loader-result-cache";

export type ArchivedModelsLoaderOptions = {
  teamId: string;
  modelsPerLoad?: number;
  loadThreshold?: number;
  autoLoadOnMount?: boolean;
  workspaceKey?: string;
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
  } = options;
  const cacheKey = `issue_${teamId}_excludeTrash_`;
  const [models, setModels] = useState<Issue[]>([]);
  const [loading, setLoading] = useState(false);
  const [hasMore, setHasMore] = useState(true);
  const [totalCount, setTotalCount] = useState<number | undefined>();
  const [error, setError] = useState<string | undefined>();
  const cursorRef = useRef<string | undefined>(undefined);
  const hasMoreRef = useRef(true);
  const inFlight = useRef(false);

  const fetchPage = useCallback(
    async (reset: boolean) => {
      if (inFlight.current) return;
      if (!reset && !hasMoreRef.current) return;
      inFlight.current = true;
      setLoading(true);
      setError(undefined);
      try {
        if (reset) {
          const cached = issueCache.get(cacheKey);
          if (cached) {
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
          }
        }
        const page = await listIssueRecords(
          {
            teamId,
            archived: "true",
            limit: modelsPerLoad,
            cursor: reset ? undefined : cursorRef.current,
            includeTotal: true,
            sort: "updatedAt",
            direction: "desc",
          },
          undefined,
          workspaceKey,
        );
        const nextHasMore = Boolean(page.hasMore && page.nextCursor);
        cursorRef.current = page.nextCursor;
        hasMoreRef.current = nextHasMore;
        setHasMore(nextHasMore);
        setTotalCount(page.total);
        setModels((current) => {
          const merged = reset ? page.items : [...current, ...page.items];
          const seen = new Set<string>();
          const deduped = merged.filter((item) => {
            if (seen.has(item.id)) return false;
            seen.add(item.id);
            return true;
          });
          issueCache.set(cacheKey, {
            models: deduped,
            cursor: page.nextCursor,
            hasMore: nextHasMore,
            totalCount: page.total,
          });
          return deduped;
        });
      } catch (cause) {
        setError(
          cause instanceof Error
            ? cause.message
            : "Could not load archived issues",
        );
      } finally {
        inFlight.current = false;
        setLoading(false);
      }
    },
    [cacheKey, modelsPerLoad, teamId, workspaceKey],
  );

  useEffect(() => {
    if (!autoLoadOnMount || !teamId) return;
    void fetchPage(true);
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
    void fetchPage(true);
  }, [cacheKey, fetchPage]);

  return {
    loading,
    models,
    totalCount,
    hasMore,
    error,
    loadMore,
    reload,
  };
}
