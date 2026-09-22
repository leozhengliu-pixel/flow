import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  OnboardingNavigationProvider,
  OnboardingStepLayout,
  useOnboardingNavigation,
} from "./onboarding-step-layout";

function Probe() {
  const nav = useOnboardingNavigation();
  return <span data-testid="step">{nav.step}</span>;
}

describe("OnboardingStepLayout", () => {
  it("renders Skip/Continue and wires navigation provider", async () => {
    const user = userEvent.setup();
    const nextStep = vi.fn();
    const skipStep = vi.fn();
    render(
      <OnboardingNavigationProvider
        step={0}
        count={3}
        stepLabels={["A", "B", "C"]}
        goToStep={vi.fn()}
        nextStep={nextStep}
        skipStep={skipStep}
      >
        <OnboardingStepLayout title="Profile" description="Tell us who you are">
          <Probe />
        </OnboardingStepLayout>
      </OnboardingNavigationProvider>,
    );
    expect(screen.getByText("Profile")).toBeInTheDocument();
    expect(screen.getByTestId("step")).toHaveTextContent("0");
    await user.click(screen.getByRole("button", { name: "Skip" }));
    expect(skipStep).toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Continue" }));
    expect(nextStep).toHaveBeenCalled();
  });

  it("throws outside the provider", () => {
    expect(() => render(<Probe />)).toThrow(
      /must be used within OnboardingNavigationProvider/,
    );
  });
});
