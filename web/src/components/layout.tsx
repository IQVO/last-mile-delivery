import type { ReactNode } from "react";
import { DataTable, StatusPill } from "@warehouse/ui-kit";
import type { RouteState, WaveState } from "../types";

export function PageHeader({ title, subtitle }: { title: string; subtitle: ReactNode }) {
  return (
    <div>
      <h1 style={{ fontSize: "var(--wh-font-size-2xl)", margin: 0 }}>{title}</h1>
      <p style={{ color: "var(--wh-color-text-muted)", marginTop: 4 }}>{subtitle}</p>
    </div>
  );
}

export function Stack({ children, gap = 4 }: { children: ReactNode; gap?: 3 | 4 | 5 }) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: `var(--wh-space-${gap})` }}>
      {children}
    </div>
  );
}

/** A label/value list (dl) for a record's scalar fields. */
export function Facts({ items }: { items: { label: string; value: ReactNode }[] }) {
  return (
    <dl
      style={{
        display: "grid",
        gridTemplateColumns: "max-content 1fr",
        gap: "var(--wh-space-2) var(--wh-space-4)",
        margin: 0,
        fontSize: "var(--wh-font-size-sm)",
      }}
    >
      {items.map((item) => (
        <div key={item.label} style={{ display: "contents" }}>
          <dt style={{ color: "var(--wh-color-text-muted)" }}>{item.label}</dt>
          <dd style={{ margin: 0 }}>{item.value}</dd>
        </div>
      ))}
    </dl>
  );
}

type Tone = "neutral" | "progress" | "success" | "warning" | "danger";

/** A wave's lifecycle tone: an open or running wave is progress, a finished
 *  one success, a cancelled one danger. */
const WAVE_TONE: Record<WaveState, Tone> = {
  OPEN: "progress",
  INDUCTION_CLOSED: "progress",
  DISPATCHED: "warning",
  COMPLETED: "success",
  CANCELLED: "danger",
};

const ROUTE_TONE: Record<RouteState, Tone> = {
  PLANNED: "neutral",
  OUT_FOR_DELIVERY: "progress",
  COMPLETED: "success",
};

export function WaveStatePill({ state }: { state: WaveState }) {
  return <StatusPill status={state} tone={WAVE_TONE[state]} size="sm" />;
}

export function RouteStatePill({ state }: { state: RouteState }) {
  return <StatusPill status={state} tone={ROUTE_TONE[state]} size="sm" />;
}

/** The stops of a wave: the packages of each stopKey. */
export function WaveStopsTable({ stops }: { stops: { stopKey: string; destinationPostalPrefix?: string; trackingNumbers: string[] }[] }) {
  return (
    <DataTable
      rowKey={(s) => s.stopKey}
      rows={stops}
      columns={[
        { key: "stop", header: "Stop key", render: (s) => s.stopKey },
        { key: "prefix", header: "Postal prefix", render: (s) => s.destinationPostalPrefix ?? "—" },
        { key: "packages", header: "Packages", render: (s) => s.trackingNumbers.join(", ") },
        { key: "count", header: "Count", align: "right", render: (s) => String(s.trackingNumbers.length) },
      ]}
    />
  );
}
