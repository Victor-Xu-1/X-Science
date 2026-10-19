import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import React from "react";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import XScienceSessionOptionsMenu from "@/renderer/components/xScience/runtime/XScienceSessionOptionsMenu";
import { renderWithI18n } from "../i18nTestUtils";

vi.mock("@/renderer/services/xScienceCatalog", () => ({
  loadXScienceAssistants: vi.fn().mockResolvedValue([]),
}));
vi.mock("@/renderer/services/xScienceCompute", () => ({
  loadXScienceComputeProviders: vi.fn().mockResolvedValue([]),
  loadXScienceSessionComputeProviders: vi.fn().mockResolvedValue([]),
}));

const value = {
  delegation: false,
  autoReview: false,
  memory: false,
  targetAgent: "OPERON",
  goalText: null,
  asRoutine: false,
};
const change = vi.fn();
const localChange = vi.fn();
const refresh = vi.fn();
async function open(overrides: Record<string, unknown> = {}) {
  // Structural spread deliberately works with the old menu: these RED checks
  // fail on real checked/click behavior, not a missing new module or TS prop.
  const props = {
    delegationControl: {
      enabled: true,
      pending: false,
      available: true,
      hasLiveAttempts: false,
      error: null,
      retryable: false,
      change,
      retry: vi.fn(),
      refresh,
      current: () => true,
      ...overrides,
    },
  };
  await renderWithI18n(
    <MemoryRouter>
      <XScienceSessionOptionsMenu
        rootFrameId="delegation-root"
        value={value}
        onChange={localChange}
        {...props}
      />
    </MemoryRouter>
  );
  fireEvent.click(screen.getByRole("button", { name: "会话选项" }));
  return screen.findByTestId("session-config-row-delegation");
}

describe("authoritative delegation menu control", () => {
  beforeEach(() => vi.clearAllMocks());
  afterEach(cleanup);
  it("renders the current policy instead of a late legacy session option", async () => {
    const row = await open();
    expect(row).toHaveAttribute("aria-checked", "true");
    expect(refresh).toHaveBeenCalled();
  });
  it("submits only through the policy owner rather than flipping the local option", async () => {
    fireEvent.click(await open());
    await waitFor(() => expect(change).toHaveBeenCalledWith(false));
    expect(localChange).not.toHaveBeenCalled();
  });
  it("keeps an unknown policy indeterminate and disables only that control", async () => {
    const row = await open({ enabled: undefined, pending: true });
    expect(row).toHaveAttribute("aria-checked", "mixed");
    expect(row).toBeDisabled();
    expect(
      screen.getByTestId("session-config-row-autoReview")
    ).not.toBeDisabled();
  });
});
