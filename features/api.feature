Feature: The REST contract of the delivery station
  Every operation of apis/openapi.yaml over the real router and the in-memory
  doubles, with the Idempotency-Key middleware in place: the list and read
  endpoints, the problem slugs of the validation and 404 cases, and the replay
  of a repeated idempotency key (no second event).

  Background:
    Given a delivery station "node-ds-01"

  Scenario: The waves of a station and date are listed by induct cutoff
    Given a wave "wave-1" with its cutoff in 3 hours and capacity 5
    And a wave "wave-2" with its cutoff in 2 hours and capacity 5
    When the client requests "/waves?stationNodeId=node-ds-01&serviceDate=2026-10-24"
    Then the response is 200
    And the response field "items.0.waveId" is "wave-2"
    And the response field "items.1.waveId" is "wave-1"

  Scenario: A station with no wave on the date lists nothing
    When the client requests "/waves?stationNodeId=node-ds-01&serviceDate=2026-10-24"
    Then the response is 200
    And the response has 0 items

  Scenario: Listing waves without a station is refused
    When the client requests "/waves?serviceDate=2026-10-24"
    Then the response is 422
    And the response problem is "validation-failed"

  Scenario: The stops of a wave group its packages by stop key
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And the label of "TRK-1" says stop "k-a"
    And the label of "TRK-2" says stop "k-a"
    And the packages "TRK-1" and "TRK-2" arrive at the station
    And the package "TRK-3" arrives at the station
    When the client requests "/waves/wave-1/stops"
    Then the response is 200
    And the stop "k-a" holds the packages "TRK-1" and "TRK-2"
    And the stop "pkg:TRK-3" holds the packages "TRK-3"

  Scenario: The stops of an unknown wave are not found
    When the client requests "/waves/wave-404/stops"
    Then the response is 404
    And the response problem is "not-found"

  Scenario: A route reads back with its attempts
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And a partner "dsp-1" with a van of 2
    And a started route "route-1" with the packages "TRK-1" and "TRK-2"
    And the driver records the package "TRK-1" as delivered
    When the client requests "/routes/route-1"
    Then the response is 200
    And the response field "routeId" is "route-1"
    And the response field "state" is "OUT_FOR_DELIVERY"
    And the response has 1 attempts

  Scenario: An unknown route is not found
    When the client requests "/routes/route-404"
    Then the response is 404
    And the response problem is "not-found"

  Scenario: A state changing POST needs an idempotency key
    When the client sends a "POST" to "/partners" with body:
      """
      {"stationNodeId":"node-ds-01","vanCapacity":2}
      """
    Then the response is 400
    And the response problem is "idempotency-key-required"

  Scenario: A repeated key replays the stored response and publishes no second event
    When the client sends a "POST" to "/waves" with key "k-1" and body:
      """
      {"stationNodeId":"node-ds-01","serviceDate":"2026-10-24","inductCutoffAt":"2026-10-24T09:00:00Z","windowStart":"2026-10-24T12:00:00Z","windowEnd":"2026-10-24T20:00:00Z","stopCapacity":5}
      """
    Then the response is 201
    And the response field "waveId" is "wave-1"
    And exactly 1 "DeliveryWaveScheduled" event was published
    When the same request is sent again
    Then the response is 201
    And the response replays the stored response
    And exactly 1 "DeliveryWaveScheduled" event was published

  Scenario: A key reused with another body is refused
    When the client sends a "POST" to "/waves" with key "k-1" and body:
      """
      {"stationNodeId":"node-ds-01","serviceDate":"2026-10-24","inductCutoffAt":"2026-10-24T09:00:00Z","windowStart":"2026-10-24T12:00:00Z","windowEnd":"2026-10-24T20:00:00Z","stopCapacity":5}
      """
    Then the response is 201
    When the client sends a "POST" to "/waves" with key "k-1" and body:
      """
      {"stationNodeId":"node-ds-01","serviceDate":"2026-10-24","inductCutoffAt":"2026-10-24T09:00:00Z","windowStart":"2026-10-24T12:00:00Z","windowEnd":"2026-10-24T20:00:00Z","stopCapacity":9}
      """
    Then the response is 422
    And the response problem is "idempotency-key-reused"

  Scenario: A body that is not JSON fails validation
    When the client sends a "POST" to "/partners" with key "k-2" and body:
      """
      {not json}
      """
    Then the response is 422
    And the response problem is "validation-failed"

  Scenario: Cancelling a wave needs a reason
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    When the client sends a "POST" to "/waves/wave-1:cancel" with key "k-3" and body:
      """
      {"reason":""}
      """
    Then the response is 422
    And the response problem is "validation-failed"
