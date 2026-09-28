import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import {
  clearSelectedModels,
  setSelectedModels,
} from "@/lib/selected-models-store";
import {
  getAllowedActionGroups,
  useActionGroupsForSelection,
} from "./use-action-groups-for-selection";

describe("useActionGroupsForSelection (LS-0721)", () => {
  afterEach(() => {
    clearSelectedModels();
  });

  it("publishes allowedActionGroups when selectedModels is non-empty", () => {
    const { result, unmount } = renderHook(() =>
      useActionGroupsForSelection(["Issues", "Projects"]),
    );
    expect(result.current).toEqual([]);
    expect(getAllowedActionGroups()).toEqual([]);

    act(() => setSelectedModels(["Issue"]));
    expect(result.current).toEqual(["Issues", "Projects"]);
    expect(getAllowedActionGroups()).toEqual(["Issues", "Projects"]);

    unmount();
    expect(getAllowedActionGroups()).toEqual([]);
  });

  it("clears allow-list when selection empties", () => {
    const { result } = renderHook(() =>
      useActionGroupsForSelection("Issues"),
    );
    act(() => setSelectedModels(["Issue"]));
    expect(result.current).toEqual(["Issues"]);
    act(() => clearSelectedModels());
    expect(result.current).toEqual([]);
  });
});
