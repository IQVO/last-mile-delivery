import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebar: SidebarsConfig = {
  apisidebar: [
    {
      type: "doc",
      id: "api-reference/rest/last-mile-delivery-api",
    },
    {
      type: "category",
      label: "Waves",
      link: {
        type: "doc",
        id: "api-reference/rest/waves",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/schedule-wave",
          label: "Schedule a delivery wave (ScheduleWave)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/list-waves",
          label: "List the waves of a station for a service date",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-wave-stops",
          label: "List the stops of a wave (packages grouped by stopKey)",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/close-induction",
          label: "Close induction of a wave (CloseInduction)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/dispatch-wave",
          label: "Dispatch a wave (DispatchWave)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/cancel-wave",
          label: "Cancel a wave (CancelWave)",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "Routes",
      link: {
        type: "doc",
        id: "api-reference/rest/routes",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/plan-route",
          label: "Plan a DSP van route in a wave (PlanRoute)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-route",
          label: "Get a route with its stops and attempts",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/start-route",
          label: "Start a route (StartRoute)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/record-attempt",
          label: "Record a delivery attempt (RecordAttempt)",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "Partners",
      link: {
        type: "doc",
        id: "api-reference/rest/partners",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/contract-partner",
          label: "Contract a delivery service partner (ContractPartner)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/deactivate-partner",
          label: "Deactivate a delivery service partner (DeactivatePartner)",
          className: "api-method post",
        },
      ],
    },
  ],
};

export default sidebar.apisidebar;
