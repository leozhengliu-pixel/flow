export const WELCOME_STEPS = [
  "profile",
  "invite",
  "github",
  "slack",
] as const;

export type WelcomeStepId = (typeof WELCOME_STEPS)[number];
