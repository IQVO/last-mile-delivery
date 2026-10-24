import { MemoryRouter } from "react-router-dom";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { WavesScreen } from "./WavesScreen";
import { json, mockApi, problem } from "../test/fetchMock";
import { cancelledWave, wave } from "../test/fixtures";

function renderScreen() {
  return render(
    <MemoryRouter>
      <WavesScreen />
    </MemoryRouter>,
  );
}

describe("WavesScreen", () => {
  it("lists the waves of the station and service date after the search", async () => {
    const api = mockApi({ "GET /waves": json({ items: [wave(), cancelledWave()] }) });
    renderScreen();
    const user = userEvent.setup();
    await user.clear(screen.getByLabelText(/Station/));
    await user.type(screen.getByLabelText(/Station/), "node-ds-01");
    await user.type(screen.getByLabelText(/Service date/), "2026-10-24");
    await user.click(screen.getByRole("button", { name: "Show waves" }));
    expect(await screen.findByText("wave-1")).toBeInTheDocument();
    expect(screen.getByText("wave-2")).toBeInTheDocument();
    expect(screen.getByText("station closed")).toBeInTheDocument();
    expect(screen.getByText("2 of 5 stops assigned")).toBeInTheDocument();
    expect(api.calls[0].query.get("stationNodeId")).toBe("node-ds-01");
    expect(api.calls[0].query.get("serviceDate")).toBe("2026-10-24");
  });

  it("asks for the input it still needs", async () => {
    renderScreen();
    expect(screen.getByText("Enter a service date.")).toBeInTheDocument();
    const button = screen.getByRole("button", { name: "Show waves" });
    expect(button).toBeDisabled();
  });

  it("shows an empty note for a station and date with no wave", async () => {
    mockApi({ "GET /waves": json({ items: [] }) });
    renderScreen();
    const user = userEvent.setup();
    await user.type(screen.getByLabelText(/Station/), "node-ds-01");
    await user.type(screen.getByLabelText(/Service date/), "2026-10-24");
    await user.click(screen.getByRole("button", { name: "Show waves" }));
    expect(await screen.findByText("No wave is scheduled for this station and date.")).toBeInTheDocument();
  });

  it("renders the service's problem when the search fails", async () => {
    mockApi({ "GET /waves": problem(422, "validation-failed", "Validation failed", "stationNodeId and a YYYY-MM-DD serviceDate are required") });
    renderScreen();
    const user = userEvent.setup();
    await user.type(screen.getByLabelText(/Station/), "node-ds-01");
    await user.type(screen.getByLabelText(/Service date/), "2026-10-24");
    await user.click(screen.getByRole("button", { name: "Show waves" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Validation failed");
  });
});
