import { createContext, useContext } from "react";

export type OnboardingNavigationValue = {
  step: number;
  count: number;
  stepLabels: string[];
  goToStep: (step: number) => void;
  nextStep: () => void;
  skipStep: () => void;
};

export const OnboardingNavigationContext =
  createContext<OnboardingNavigationValue | null>(null);

export function useOnboardingNavigation(): OnboardingNavigationValue {
  const value = useContext(OnboardingNavigationContext);
  if (!value) {
    throw new Error(
      "useOnboardingNavigation must be used within OnboardingNavigationProvider",
    );
  }
  return value;
}
