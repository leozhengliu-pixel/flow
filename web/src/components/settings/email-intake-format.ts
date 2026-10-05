import type { EmailIntakeAddress } from "@/types/flow";

/** Linear's intake title: the sender name, else the address mail is sent to. */
export function intakeTitle(address: EmailIntakeAddress) {
  return address.senderName || address.forwardingEmailAddress || address.address;
}
export function intakeDescription(address: EmailIntakeAddress) {
  if (address.senderName) return address.forwardingEmailAddress || address.address;
  return address.forwardingEmailAddress ? address.address : undefined;
}
