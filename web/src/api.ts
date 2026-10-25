import { DELIVERY_API_BASE } from "./config";
import type { DeliveryWave, Route, WaveStop } from "./types";

/** RFC 9457 problem+json body every error response from last-mile-delivery
 *  returns (`type` is a slug, e.g. `not-found`). */
export interface ProblemDetails {
  type?: string;
  title?: string;
  status?: number;
  detail?: string;
  instance?: string;
}

/**
 * Every failed call -- an HTTP error with a problem+json body, an HTTP error
 * without one, or a network failure -- surfaces as one ApiError, so a screen
 * has a single shape to render: `title` (the problem's category) and `detail`
 * (what exactly was wrong), plus `slug` to branch on a specific problem
 * (e.g. `not-found`, `validation-failed`).
 */
export class ApiError extends Error {
  readonly status: number;
  readonly problem: ProblemDetails | null;
  readonly title: string;
  readonly detail: string;
  /** The problem's `type` slug (e.g. "not-found"); "" when unknown. */
  readonly slug: string;

  constructor(status: number, problem: ProblemDetails | null, fallbackTitle: string) {
    const title = problem?.title || fallbackTitle;
    const detail = problem?.detail ?? "";
    super(detail || title);
    this.name = "ApiError";
    this.status = status;
    this.problem = problem;
    this.title = title;
    this.detail = detail;
    this.slug = problem?.type ?? "";
  }
}

/** Normalizes anything thrown by a request into an ApiError. */
export function toApiError(err: unknown): ApiError {
  if (err instanceof ApiError) return err;
  const message = err instanceof Error ? err.message : String(err);
  return new ApiError(0, { detail: message }, "Network error");
}

type Query = Record<string, string | number | boolean | undefined>;

export function withQuery(path: string, query?: Query): string {
  if (!query) return path;
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined && value !== "") params.set(key, String(value));
  }
  const qs = params.toString();
  return qs ? `${path}?${qs}` : path;
}

/**
 * The one door to the service. Only this context's own API is ever called
 * (`DELIVERY_API_BASE` = <apiOrigin>/api/last-mile-delivery): no sibling
 * context. This remote is READ-ONLY -- the three GETs of the contract and
 * nothing else; scheduling, planning and recording attempts stay on the
 * operator tooling that owns them. GETs send no custom header, so they stay
 * simple CORS requests.
 */
async function get<T>(path: string): Promise<T> {
  let res: Response;
  try {
    res = await fetch(`${DELIVERY_API_BASE}${path}`, { method: "GET" });
  } catch (err) {
    throw toApiError(err);
  }
  if (!res.ok) {
    let problem: ProblemDetails | null = null;
    try {
      problem = (await res.json()) as ProblemDetails;
    } catch {
      // non-JSON error body -- fall through with problem = null
    }
    throw new ApiError(res.status, problem, `${res.status} ${res.statusText}`.trim());
  }
  return (await res.json()) as T;
}

const seg = encodeURIComponent;

/** A waves page as the UI uses it: the item list is never undefined. */
function normalizeWavePage(body: { items?: DeliveryWave[] }): { items: DeliveryWave[] } {
  return { items: body.items ?? [] };
}

/** GET /waves?stationNodeId=&serviceDate= -- ordered by inductCutoffAt. */
export async function listWaves(stationNodeId: string, serviceDate: string): Promise<{ items: DeliveryWave[] }> {
  return normalizeWavePage(
    await get<{ items?: DeliveryWave[] }>(withQuery("/waves", { stationNodeId, serviceDate })),
  );
}

/** GET /waves/{waveId}/stops -- packages grouped by stopKey, first induction first. */
export async function getWaveStops(waveId: string): Promise<{ items: WaveStop[] }> {
  const body = await get<{ items?: WaveStop[] }>(`/waves/${seg(waveId)}/stops`);
  return { items: (body.items ?? []).map((s) => ({ ...s, trackingNumbers: s.trackingNumbers ?? [] })) };
}

/** GET /routes/{routeId} -- the route with its stops and attempts. */
export async function getRoute(routeId: string): Promise<Route> {
  const r = await get<Route>(`/routes/${seg(routeId)}`);
  return { ...r, stops: r.stops ?? [], attempts: r.attempts ?? [] };
}
