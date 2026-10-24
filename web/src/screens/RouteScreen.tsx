import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { Card, DataTable } from "@warehouse/ui-kit";
import { getRoute } from "../api";
import { Form, FormRow, SubmitButton, TextField } from "../components/formkit";
import { Facts, PageHeader, RouteStatePill, Stack } from "../components/layout";
import { RequestView } from "../components/ProblemAlert";
import { useRequest } from "../hooks/useRequest";
import { idProblem, utcLabel } from "../lib";
import type { Attempt, Route as DeliveryRoute, Stop } from "../types";

/** `routes`: a lookup by route id, then `routes/:routeId`. */
export function RouteLookupScreen() {
  const [routeId, setRouteId] = useState("");
  const navigate = useNavigate();
  const id = routeId.trim();
  const problem = id === "" ? null : idProblem("route", id);
  const go = () => {
    if (id !== "" && problem === null) navigate(encodeURIComponent(id));
  };
  return (
    <Stack gap={5}>
      <PageHeader title="Routes" subtitle="A delivery service partner's van route: its stops and every recorded attempt" />
      <Card title="Look up a route">
        <Form label="Route lookup" onSubmit={go}>
          <FormRow>
            <TextField
              label="Route"
              value={routeId}
              onChange={setRouteId}
              placeholder="route-1"
              hint={problem ?? "The route id of a planned or running delivery route"}
            />
            <SubmitButton disabled={id === "" || problem !== null}>View route</SubmitButton>
          </FormRow>
        </Form>
      </Card>
    </Stack>
  );
}

/** The route element of `routes/:routeId`. */
export function RouteScreen() {
  const { routeId } = useParams();
  return routeId ? <DeliveryRouteDetail routeId={routeId} /> : null;
}

function DeliveryRouteDetail({ routeId }: { routeId: string }) {
  const route = useRequest(() => getRoute(routeId), `route|${routeId}`);
  return (
    <Stack gap={5}>
      <PageHeader title={`Route ${routeId}`} subtitle="The van route, its stops with their derived outcome and the delivery attempts" />
      <RequestView state={route} what="the route">
        {(r) => <RouteBody route={r} />}
      </RequestView>
      <Link to="../routes">← Look up another route</Link>
    </Stack>
  );
}

function RouteBody({ route }: { route: DeliveryRoute }) {
  return (
    <>
      <Card title="Route">
        <Facts
          items={[
            { label: "State", value: <RouteStatePill state={route.state} /> },
            { label: "Wave", value: route.waveId },
            { label: "Partner", value: route.dspId },
            { label: "Station", value: route.stationNodeId },
            { label: "Stops", value: String(route.stopCount) },
          ]}
        />
      </Card>
      <Card title="Stops">
        <StopsTable stops={route.stops} />
      </Card>
      <Card title="Attempts">
        {route.attempts.length === 0 ? "No delivery attempt recorded yet." : <AttemptsTable attempts={route.attempts} />}
      </Card>
    </>
  );
}

function StopsTable({ stops }: { stops: Stop[] }) {
  return (
    <DataTable
      rowKey={(s) => String(s.stopNo)}
      rows={stops}
      columns={[
        { key: "no", header: "Stop", align: "right", render: (s) => String(s.stopNo) },
        { key: "packages", header: "Packages", render: (s) => s.trackingNumbers.join(", ") },
        { key: "outcome", header: "Outcome", render: (s) => s.outcome ?? "pending" },
      ]}
    />
  );
}

function AttemptsTable({ attempts }: { attempts: Attempt[] }) {
  return (
    <DataTable
      rowKey={(a) => `${a.trackingNumber}-${a.at}`}
      rows={attempts}
      columns={[
        { key: "package", header: "Package", render: (a) => a.trackingNumber },
        { key: "outcome", header: "Outcome", render: (a) => a.outcome },
        { key: "reason", header: "Failure reason", render: (a) => a.failureReason ?? "—" },
        { key: "at", header: "Recorded", render: (a) => utcLabel(a.at) },
      ]}
    />
  );
}
