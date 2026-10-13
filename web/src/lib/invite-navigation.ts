/** Opens Flow's invite flow (the members page with the "Invite people" dialog open) from "Invite and add/assign…" rows. */
export function openMemberInvite() {
  const workspace = location.pathname.split('/').filter(Boolean)[0]
  if (workspace) location.assign(`/${workspace}/members?invite=1`)
}
