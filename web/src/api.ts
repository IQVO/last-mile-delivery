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

/** One page of a collection: `nextCursor` is absent on the last page. */
interface Page<T> {
  items?: T[];
  nextCursor?: string;
}

/** Safety bound of the cursor loop (pages of PAGE_LIMIT rows each). */
const MAX_PAGES = 200;
const PAGE_LIMIT = 500;

/** Reads every page of a collection: follows `nextCursor` until it is absent. */
async function getAll<T>(path: string, query: Record<string, string | undefined> = {}): Promise<T[]> {
  const items: T[] = [];
  let cursor: string | undefined;
  for (let page = 0; page < MAX_PAGES; page++) {
    const body = await get<Page<T>>(withQuery(path, { ...query, limit: PAGE_LIMIT, cursor }));
    items.push(...(body.items ?? []));
    if (!body.nextCursor) return items;
    cursor = body.nextCursor;
  }
  throw new ApiError(0, null, `${path}: more than ${MAX_PAGES} pages`);
}

/** GET /waves?stationNodeId=&serviceDate= -- every page, ordered by inductCutoffAt. */
export async function listWaves(stationNodeId: string, serviceDate: string): Promise<{ items: DeliveryWave[] }> {
  return { items: await getAll<DeliveryWave>("/waves", { stationNodeId, serviceDate }) };
}

/** GET /waves/{waveId}/stops -- every page, packages grouped by stopKey, first induction first. */
export async function getWaveStops(waveId: string): Promise<{ items: WaveStop[] }> {
  const items = await getAll<WaveStop>(`/waves/${seg(waveId)}/stops`);
  return { items: items.map((s) => ({ ...s, trackingNumbers: s.trackingNumbers ?? [] })) };
}

/** GET /routes/{routeId} -- the route with its stops and attempts. */
export async function getRoute(routeId: string): Promise<Route> {
  const r = await get<Route>(`/routes/${seg(routeId)}`);
  return { ...r, stops: r.stops ?? [], attempts: r.attempts ?? [] };
}
