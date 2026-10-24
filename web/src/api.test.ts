import { describe, expect, it } from "vitest";
import { ApiError, getRoute, getWaveStops, listWaves, toApiError, withQuery } from "./api";
import { DELIVERY_API_BASE } from "./config";
import { json, mockApi, problem } from "./test/fetchMock";
import { route, wave, waveStop } from "./test/fixtures";

describe("withQuery", () => {
  it("drops empty values and encodes the rest", () => {
    expect(withQuery("/waves", { stationNodeId: "node-ds-01", serviceDate: "2026-10-24" })).toBe(
      "/waves?stationNodeId=node-ds-01&serviceDate=2026-10-24",
    );
    expect(withQuery("/waves", {})).toBe("/waves");
    expect(withQuery("/waves")).toBe("/waves");
  });
});

describe("the API base", () => {
  it("is this context's own /api/last-mile-delivery or the developer port, never a sibling", () => {
    expect(DELIVERY_API_BASE).toBe("http://localhost:8080");
  });
});

describe("requests", () => {
  it("lists the waves of a station and service date with no custom header", async () => {
    const api = mockApi({ "GET /waves": json({ items: [wave()] }) });
    const page = await listWaves("node-ds-01", "2026-10-24");
    expect(page.items).toHaveLength(1);
    const call = api.calls[0];
    expect(call.url).toBe(`${DELIVERY_API_BASE}/waves?stationNodeId=node-ds-01&serviceDate=2026-10-24`);
    expect(call.headers).toEqual({});
  });

  it("normalizes a missing item list", async () => {
    mockApi({ "GET /waves": json({}) });
    expect(await listWaves("node-ds-01", "2026-10-24")).toEqual({ items: [] });
  });

  it("reads the stops of a wave by escaped id", async () => {
    const api = mockApi({ "GET /waves/wave%201/stops": json({ items: [{ ...waveStop(), trackingNumbers: undefined as never }] }) });
    const page = await getWaveStops("wave 1");
    expect(page.items[0].trackingNumbers).toEqual([]);
    expect(api.calls[0].path).toBe("/waves/wave%201/stops");
  });

  it("reads one route and normalizes missing lists", async () => {
    mockApi({ "GET /routes/route-1": json({ ...route(), stops: undefined as never, attempts: undefined as never }) });
    const r = await getRoute("route-1");
    expect(r.routeId).toBe("route-1");
    expect(r.stops).toEqual([]);
    expect(r.attempts).toEqual([]);
  });

  it("calls nothing but this context's API", async () => {
    const api = mockApi({
      "GET /waves": json({ items: [] }),
      "GET /waves/wave-1/stops": json({ items: [] }),
      "GET /routes/route-1": json(route()),
    });
    await listWaves("node-ds-01", "2026-10-24");
    await getWaveStops("wave-1");
    await getRoute("route-1");
    for (const c of api.calls) expect(c.url.startsWith(DELIVERY_API_BASE + "/")).toBe(true);
  });
});

describe("errors", () => {
  it("keeps the problem's title, detail and slug", async () => {
    mockApi({ "GET /routes/route-9": problem(404, "not-found", "Resource not found", "no route carries this id") });
    const err = await getRoute("route-9").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    const e = err as ApiError;
    expect([e.status, e.slug, e.title, e.detail]).toEqual([404, "not-found", "Resource not found", "no route carries this id"]);
    expect(e.message).toBe("no route carries this id");
  });

  it("falls back to the status text for a body that is not a problem", async () => {
    mockApi({ "GET /routes/x": new Response("<html>", { status: 502, statusText: "Bad Gateway" }) });
    const e = (await getRoute("x").catch((err: unknown) => err)) as ApiError;
    expect([e.status, e.title, e.slug, e.problem]).toEqual([502, "502 Bad Gateway", "", null]);
  });

  it("turns a network failure into an ApiError with status 0", async () => {
    mockApi({});
    const e = (await getRoute("x").catch((err: unknown) => err)) as ApiError;
    expect(e.status).toBe(0);
    expect(e.title).toBe("Network error");
    expect(toApiError(new Error("boom")).detail).toBe("boom");
    expect(toApiError("text").detail).toBe("text");
    expect(toApiError(e)).toBe(e);
  });
});
