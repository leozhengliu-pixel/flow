import { toast } from "sonner";
import { cancelWorkspaceDeletion } from "@/lib/api";
import type { Workspace } from "@/types/flow";

/** LS-0443 — cancel scheduled workspace deletion with Linear-parity toasts. */
export async function cancelOrganizationDeletion(
  workspaceKey: string,
  options: { onError?: () => void; onSuccess?: (workspace: Workspace) => void } = {},
): Promise<Workspace | undefined> {
  try {
    const workspace = await cancelWorkspaceDeletion(workspaceKey);
    toast.success("Workspace deletion has been canceled", {
      description: "Your workspace is no longer scheduled for deletion.",
    });
    options.onSuccess?.(workspace);
    return workspace;
  } catch (error) {
    toast.error("Workspace deletion could not be canceled", {
      description:
        error instanceof Error
          ? error.message
          : "We encountered a problem canceling the scheduled deletion of your workspace. Please try again.",
    });
    options.onError?.();
    return undefined;
  }
}
