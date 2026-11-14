Feature: REST idempotency and paging rules taken from the documentation
  Scenarios derived from the use cases (the Idempotency line of ScheduleWave,
  CloseInduction, PlanRoute, StartRoute and RecordAttempt) and ADR 0010: a repeated
  key replays the stored answer and raises no second event, the key is global, and
  the collections page with limit and a keyset cursor.

  Background:
    Given a delivery station "node-ds-01"

  # --- Idempotency-Key ---

  Scenario: Closing induction twice under one key replays the answer; under a new key it is refused
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    When the client posts to "/waves/wave-1/induction-closures" with the key "close-1"
    Then the response is 200
    When the same request is sent again
    Then the response is 200
    And the response replays the stored response
    And wave "wave-1" is "INDUCTION_CLOSED"
    When the client posts to "/waves/wave-1/induction-closures" with the key "close-2"
    Then the response is 409
    And the response problem is "illegal-transition"

  Scenario: Planning a route twice under one key plans one route and announces it once
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And a partner "dsp-1" with a van of 2
    And the packages "TRK-1" are inducted in wave "wave-1" and induction is closed
    When the client sends a "POST" to "/routes" with key "plan-1" and body:
      """
      {"waveId":"wave-1","dspId":"dsp-1","stops":[{"stopNo":1,"trackingNumbers":["TRK-1"]}]}
      """
    Then the response is 201
    And the response field "routeId" is "route-1"
    When the same request is sent again
    Then the response is 201
    And the response replays the stored response
    And the response field "routeId" is "route-1"
    And exactly 1 "RoutePlanned" event was published

  Scenario: Starting a route twice under one key announces each package once
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And a partner "dsp-1" with a van of 2
    And the packages "TRK-1" and "TRK-2" are inducted in wave "wave-1" and induction is closed
    And the planner plans a route with 2 stops
    And the planner dispatches wave "wave-1"
    When the client posts to "/routes/route-1/starts" with the key "start-1"
    Then the response is 200
    When the same request is sent again
    Then the response is 200
    And the response replays the stored response
    And exactly 2 "PackageOutForDelivery" events were published
    And exactly 2 "StopEtaWindowAssigned" events were published

  Scenario: Recording an attempt twice under one key records it once
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And a partner "dsp-1" with a van of 2
    And a started route "route-1" with the packages "TRK-1" and "TRK-2"
    When the client sends a "POST" to "/routes/route-1/attempts" with key "attempt-1" and body:
      """
      {"trackingNumber":"TRK-1","outcome":"DELIVERED","at":"2026-10-24T14:00:00Z"}
      """
    Then the response is 201
    When the same request is sent again
    Then the response is 201
    And the response replays the stored response
    And exactly 1 "DeliveryAttempted" event was published

  Scenario: One key cannot be reused on another operation
    When the client sends a "POST" to "/partners" with key "shared-key" and body:
      """
      {"stationNodeId":"node-ds-01","vanCapacity":2}
      """
    Then the response is 201
    When the client sends a "POST" to "/waves" with key "shared-key" and body:
      """
      {"stationNodeId":"node-ds-01","serviceDate":"2026-10-24","inductCutoffAt":"2026-10-24T09:00:00Z","windowStart":"2026-10-24T12:00:00Z","windowEnd":"2026-10-24T20:00:00Z","stopCapacity":5}
      """
    Then the response is 409
    And the response problem is "idempotency-key-reused"
    And no wave exists

  # --- GET /waves: the station's waves of one service date ---

  Scenario: Another service date or another station lists no wave
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And a delivery station "node-ds-02"
    When the client requests "/waves?stationNodeId=node-ds-01&serviceDate=2026-10-25"
    Then the response has 0 items
    When the client requests "/waves?stationNodeId=node-ds-02&serviceDate=2026-10-24"
    Then the response has 0 items

  Scenario: A service date that is not a calendar day is refused
    When the client requests "/waves?stationNodeId=node-ds-01&serviceDate=2026-13-01"
    Then the response is 422
    And the response problem is "validation-failed"

  # --- Keyset paging of a collection ---

  Scenario: A collection pages by limit and cursor, earliest cutoff first
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And a wave "wave-2" with its cutoff in 3 hours and capacity 5
    And a wave "wave-3" with its cutoff in 4 hours and capacity 5
    When the client requests "/waves?stationNodeId=node-ds-01&serviceDate=2026-10-24&limit=2"
    Then the response has 2 items
    And the response field "items.0.waveId" is "wave-1"
    And the response field "items.1.waveId" is "wave-2"
    When the client requests "/waves?stationNodeId=node-ds-01&serviceDate=2026-10-24&limit=2" with the cursor of the previous page
    Then the response has 1 item
    And the response field "items.0.waveId" is "wave-3"
    And the response has no field "nextCursor"

  Scenario: A last page that exactly fills the limit has no next cursor
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And a wave "wave-2" with its cutoff in 3 hours and capacity 5
    When the client requests "/waves?stationNodeId=node-ds-01&serviceDate=2026-10-24&limit=2"
    Then the response has 2 items
    And the response has no field "nextCursor"

  Scenario Outline: A limit outside 1 to 500 is refused
    When the client requests "/waves?stationNodeId=node-ds-01&serviceDate=2026-10-24&limit=<limit>"
    Then the response is 400
    And the response problem is "invalid-limit"

    Examples:
      | limit |
      | 0     |
      | 501   |
      | many  |
      | 1&limit=2 |

  Scenario: The largest limit is accepted
    When the client requests "/waves?stationNodeId=node-ds-01&serviceDate=2026-10-24&limit=500"
    Then the response is 200

  Scenario: A cursor that is not one of ours is refused
    When the client requests "/waves?stationNodeId=node-ds-01&serviceDate=2026-10-24&cursor=not-a-cursor"
    Then the response is 400
    And the response problem is "invalid-cursor"

  Scenario: A query parameter the collection does not declare is refused
    When the client requests "/waves?stationNodeId=node-ds-01&serviceDate=2026-10-24&offset=10"
    Then the response is 400
    And the response problem is "unknown-query-parameter"
