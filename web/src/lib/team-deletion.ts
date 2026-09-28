/** Days a deleted team stays restorable before it is permanently removed. */
export const TEAM_RESTORE_DAYS = 30;

/** Confirmation copy shared by every place a team can be deleted. */
export const DELETE_TEAM_DESCRIPTION = `The team and its issues will be hidden right away and permanently deleted after ${TEAM_RESTORE_DAYS} days. Until then, workspace admins can restore it from Teams › Recently deleted.`;
