import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderHook, waitFor, act } from "@testing-library/react";
import { listIssueRecords } from "@/lib/api";
import { useArchivedModelsLoader } from "./use-archived-models-loader";

vi.mock("@/lib/api", () => ({
  listIssueRecords: vi.fn(),
}));

describe("useArchivedModelsLoader", () => {
  beforeEach(() => {
    vi.mocked(listIssueRecords).mockReset();
  });

  it("loads archived issues with cursor and threshold loadMore", async () => {
    vi.mocked(listIssueRecords)
      .mockResolvedValueOnce({
        items: [
          { id: "1", identifier: "A-1", title: "One", archivedAt: "2026-01-01" },
        ] as never,
        hasMore: true,
        nextCursor: "c2",
        total: 2,
      })
      .mockResolvedValueOnce({
        items: [
          { id: "2", identifier: "A-2", title: "Two", archivedAt: "2026-01-02" },
        ] as never,
        hasMore: false,
        nextCursor: undefined,
        total: 2,
      });

    const { result } = renderHook(() =>
      useArchivedModelsLoader({ teamId: "team-1", loadThreshold: 30 }),
    );

    await waitFor(() => expect(result.current.models).toHaveLength(1));
    expect(listIssueRecords).toHaveBeenCalledWith(
      expect.objectContaining({
        teamId: "team-1",
        archived: "true",
        limit: 50,
      }),
      undefined,
      undefined,
    );

    await act(async () => {
      result.current.loadMore(10);
    });
    await waitFor(() => expect(result.current.models).toHaveLength(2));
    expect(result.current.totalCount).toBe(2);
    expect(result.current.hasMore).toBe(false);
  });
});
