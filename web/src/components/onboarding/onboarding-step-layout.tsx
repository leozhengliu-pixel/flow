import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  type ReactNode,
} from "react";
import "./onboarding-step-layout.css";

export type OnboardingNavigationValue = {
  step: number;
  count: number;
  stepLabels: string[];
  goToStep: (step: number) => void;
  nextStep: () => void;
  skipStep: () => void;
};

const OnboardingNavigationContext =
  createContext<OnboardingNavigationValue | null>(null);

export function OnboardingNavigationProvider({
  step,
  count,
  stepLabels,
  goToStep,
  nextStep,
  skipStep,
  children,
}: OnboardingNavigationValue & { children: ReactNode }) {
  const value = useMemo(
    () => ({ step, count, stepLabels, goToStep, nextStep, skipStep }),
    [step, count, stepLabels, goToStep, nextStep, skipStep],
  );
  return (
    <OnboardingNavigationContext.Provider value={value}>
      {children}
    </OnboardingNavigationContext.Provider>
  );
}

export function useOnboardingNavigation(): OnboardingNavigationValue {
  const value = useContext(OnboardingNavigationContext);
  if (!value) {
    throw new Error(
      "useOnboardingNavigation must be used within OnboardingNavigationProvider",
    );
  }
  return value;
}

type StepLayoutProps = {
  icon?: ReactNode;
  title: string;
  description?: string;
  children?: ReactNode;
  primaryLabel?: string;
  primaryDisabled?: boolean;
  onPrimary?: () => void | Promise<void>;
  showSkip?: boolean;
  skipLabel?: string;
  footer?: ReactNode;
};

/** LS-0439 — shared welcome-step chrome: header / content / Skip+Continue. */
export function OnboardingStepLayout({
  icon,
  title,
  description,
  children,
  primaryLabel = "Continue",
  primaryDisabled,
  onPrimary,
  showSkip = true,
  skipLabel = "Skip",
  footer,
}: StepLayoutProps) {
  const nav = useOnboardingNavigation();
  const handlePrimary = useCallback(() => {
    if (onPrimary) void onPrimary();
    else nav.nextStep();
  }, [nav, onPrimary]);

  return (
    <div className="onboarding-step-layout" data-step={nav.step}>
      <header className="onboarding-step-header">
        {icon ? <div className="onboarding-step-icon">{icon}</div> : null}
        <h1 className="onboarding-step-title">{title}</h1>
        {description ? (
          <p className="onboarding-step-description">{description}</p>
        ) : null}
        {nav.count > 1 ? (
          <ol className="onboarding-step-dots" aria-label="Onboarding progress">
            {Array.from({ length: nav.count }, (_, index) => (
              <li
                key={nav.stepLabels[index] ?? index}
                aria-current={index === nav.step ? "step" : undefined}
                className={
                  index === nav.step
                    ? "is-active"
                    : index < nav.step
                      ? "is-done"
                      : undefined
                }
              />
            ))}
          </ol>
        ) : null}
      </header>
      <div className="onboarding-step-content">{children}</div>
      <div className="onboarding-step-actions">
        {showSkip ? (
          <button
            type="button"
            className="onboarding-step-skip"
            onClick={() => nav.skipStep()}
          >
            {skipLabel}
          </button>
        ) : (
          <span />
        )}
        <button
          type="button"
          className="onboarding-step-continue"
          disabled={primaryDisabled}
          onClick={handlePrimary}
        >
          {primaryLabel}
        </button>
      </div>
      {footer}
    </div>
  );
}
