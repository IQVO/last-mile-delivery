Feature: Route rules taken from the documentation
  Scenarios derived from the use cases (PlanRoute, StartRoute, RecordAttempt), the
  aggregate design canvas (Route), the domain events (StopEtaWindowAssigned,
  DeliveryAttempted) and ADR 0002 (A12 to A19): van capacity at the boundary, what
  a route may hold, the start events and the ETA window arithmetic, the attempt
  rules and what RetryNextWave does after a failure.

  Background:
    Given a delivery station "node-ds-01"
    And a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And a partner "dsp-1" with a van of 3

  # --- PlanRoute: van capacity (ErrRouteOverCapacity) at the boundary ---

  Scenario: A route of exactly the van's capacity in stops is accepted
    Given the packages "TRK-1", "TRK-2" and "TRK-3" are inducted in wave "wave-1" and induction is closed
    When the planner plans a route with 3 stops
    Then the request is accepted
    And exactly 1 "RoutePlanned" event was published

  Scenario: One stop more than the van's capacity is refused and nothing is announced
    Given the packages "TRK-1", "TRK-2", "TRK-3" and "TRK-4" are inducted in wave "wave-1" and induction is closed
    When the planner plans a route with 4 stops
    Then the request is refused with "route over capacity"
    And exactly 0 "RoutePlanned" events were published

  Scenario: The packages of one stop count as one stop of the van
    Given the label of "TRK-1" says stop "k-a"
    And the label of "TRK-2" says stop "k-a"
    And the packages "TRK-1", "TRK-2", "TRK-3" and "TRK-4" are inducted in wave "wave-1" and induction is closed
    When the client posts to "/routes" with a fresh key and body:
      """
      {"waveId":"wave-1","dspId":"dsp-1","stops":[{"stopNo":1,"trackingNumbers":["TRK-1","TRK-2"]},{"stopNo":2,"trackingNumbers":["TRK-3"]},{"stopNo":3,"trackingNumbers":["TRK-4"]}]}
      """
    Then the response is 201
    And the response field "stopCount" is "3"
    And the response field "state" is "PLANNED"

  # --- PlanRoute: what a route may hold (ADR 0002, A12) ---

  Scenario: A route is planned only once the wave's induction is closed
    Given the package "TRK-1" arrives at the station
    When the client posts to "/routes" with a fresh key and body:
      """
      {"waveId":"wave-1","dspId":"dsp-1","stops":[{"stopNo":1,"trackingNumbers":["TRK-1"]}]}
      """
    Then the response is 409
    And the response problem is "illegal-transition"
    And exactly 0 "RoutePlanned" events were published

  Scenario Outline: A route that breaks a planning rule is refused
    Given the packages "TRK-1" and "TRK-2" are inducted in wave "wave-1" and induction is closed
    And the package "TRK-3" arrives at the station
    When the client posts to "/routes" with a fresh key and body:
      """
      {"waveId":"<wave>","dspId":"<partner>","stops":<stops>}
      """
    Then the response is <status>
    And the response problem is "<problem>"
    And exactly 0 "RoutePlanned" events were published

    Examples:
      | rule                       | wave     | partner  | stops                                                                                   | status | problem             |
      | package not in the wave    | wave-1   | dsp-1    | [{"stopNo":1,"trackingNumbers":["TRK-3"]}]                                              | 409    | package-not-inducted |
      | gap in the stop numbers    | wave-1   | dsp-1    | [{"stopNo":1,"trackingNumbers":["TRK-1"]},{"stopNo":3,"trackingNumbers":["TRK-2"]}]     | 422    | validation-failed   |
      | duplicate stop number      | wave-1   | dsp-1    | [{"stopNo":1,"trackingNumbers":["TRK-1"]},{"stopNo":1,"trackingNumbers":["TRK-2"]}]     | 422    | validation-failed   |
      | package twice on the route | wave-1   | dsp-1    | [{"stopNo":1,"trackingNumbers":["TRK-1"]},{"stopNo":2,"trackingNumbers":["TRK-1"]}]     | 422    | validation-failed   |
      | no stop at all             | wave-1   | dsp-1    | []                                                                                      | 422    | validation-failed   |
      | a stop with no package     | wave-1   | dsp-1    | [{"stopNo":1,"trackingNumbers":[]}]                                                     | 422    | validation-failed   |
      | one stop mixing stop keys  | wave-1   | dsp-1    | [{"stopNo":1,"trackingNumbers":["TRK-1","TRK-2"]}]                                      | 409    | stop-key-mismatch   |
      | unknown wave               | wave-404 | dsp-1    | [{"stopNo":1,"trackingNumbers":["TRK-1"]}]                                              | 404    | not-found           |
      | unknown partner            | wave-1   | dsp-404  | [{"stopNo":1,"trackingNumbers":["TRK-1"]}]                                              | 404    | not-found           |

  Scenario: A package is on at most one route of a wave
    Given the packages "TRK-1" and "TRK-2" are inducted in wave "wave-1" and induction is closed
    And the planner plans a route for the packages "TRK-1"
    When the client posts to "/routes" with a fresh key and body:
      """
      {"waveId":"wave-1","dspId":"dsp-1","stops":[{"stopNo":1,"trackingNumbers":["TRK-1"]},{"stopNo":2,"trackingNumbers":["TRK-2"]}]}
      """
    Then the response is 409
    And the response problem is "package-already-routed"
    And exactly 1 "RoutePlanned" event was published

  Scenario: A partner of another station is not given the route
    Given a delivery station "node-ds-02"
    And a partner "dsp-2" for the station "node-ds-02" with a van of 3
    And the packages "TRK-1" are inducted in wave "wave-1" and induction is closed
    When the client posts to "/routes" with a fresh key and body:
      """
      {"waveId":"wave-1","dspId":"dsp-2","stops":[{"stopNo":1,"trackingNumbers":["TRK-1"]}]}
      """
    Then the response is 409
    And the response problem is "partner-station-mismatch"

  Scenario: Deactivating a partner after planning does not touch the planned route
    Given the packages "TRK-1" are inducted in wave "wave-1" and induction is closed
    And the planner plans a route with 1 stops
    And the partner "dsp-1" is deactivated
    When the planner dispatches wave "wave-1"
    And the driver starts route "route-1"
    Then the request is accepted
    And route "route-1" is "OUT_FOR_DELIVERY"

  # --- DispatchWave: the packages no route holds (A11) ---

  Scenario: A package no route holds waits at the station when no next wave exists
    Given the packages "TRK-1" and "TRK-2" are inducted in wave "wave-1" and induction is closed
    And the planner plans a route for the packages "TRK-1"
    When the planner dispatches wave "wave-1"
    Then the package "TRK-2" waits at the station
    And the package "TRK-1" is "INDUCTED"
    And exactly 1 "PackageInductionDeferred" event was published

  Scenario: A wave completes only with its last route
    Given the packages "TRK-1" and "TRK-2" are inducted in wave "wave-1" and induction is closed
    And the planner plans a route for the packages "TRK-1"
    And the planner plans a route for the packages "TRK-2"
    And the planner dispatches wave "wave-1"
    And the driver starts route "route-1"
    And the driver starts route "route-2"
    When on route "route-1" the driver records "TRK-1" as delivered
    Then route "route-1" is "COMPLETED"
    And wave "wave-1" is "DISPATCHED"
    When on route "route-2" the driver records "TRK-2" as delivered
    Then route "route-2" is "COMPLETED"
    And wave "wave-1" is "COMPLETED"

  # --- StartRoute: events and their order ---

  Scenario: A route starts only once
    Given a started route "route-1" with the packages "TRK-1" and "TRK-2"
    When the client posts to "/routes/route-1/starts" with a fresh key
    Then the response is 409
    And the response problem is "illegal-transition"
    And exactly 2 "PackageOutForDelivery" events were published
    And exactly 2 "StopEtaWindowAssigned" events were published

  Scenario: A route that does not exist cannot be started
    When the client posts to "/routes/route-404/starts" with a fresh key
    Then the response is 404
    And the response problem is "not-found"

  Scenario: Every StopEtaWindowAssigned follows every PackageOutForDelivery
    Given a started route "route-1" with the packages "TRK-1" and "TRK-2"
    Then the events of route "route-1" after RoutePlanned are, in order:
      | PackageOutForDelivery |
      | PackageOutForDelivery |
      | StopEtaWindowAssigned |
      | StopEtaWindowAssigned |

  # --- EtaWindowAtRouteStart (ADR 0013): the wave window runs 12:00 to 20:00 ---

  Scenario: Stops start before the wave window are spread over the whole window
    Given the packages "TRK-1" and "TRK-2" are inducted in wave "wave-1" and induction is closed
    And the planner plans a route with 2 stops
    And the planner dispatches wave "wave-1"
    When the driver starts route "route-1"
    Then route "route-1" gives its stop 1 the ETA window 13:00 to 15:00
    And route "route-1" gives its stop 2 the ETA window 17:00 to 19:00

  Scenario: Three stops are spread linearly, each window two hours wide
    Given the packages "TRK-1", "TRK-2" and "TRK-3" are inducted in wave "wave-1" and induction is closed
    And the planner plans a route with 3 stops
    And the planner dispatches wave "wave-1"
    When the driver starts route "route-1"
    Then route "route-1" gives its stop 1 the ETA window 12:20 to 14:20
    And route "route-1" gives its stop 2 the ETA window 15:00 to 17:00
    And route "route-1" gives its stop 3 the ETA window 17:40 to 19:40

  Scenario: Starting inside the window spreads the stops over what is left of it
    Given the packages "TRK-1" and "TRK-2" are inducted in wave "wave-1" and induction is closed
    And the planner plans a route with 2 stops
    And the planner dispatches wave "wave-1"
    And the time is 16:00
    When the driver starts route "route-1"
    Then route "route-1" gives its stop 1 the ETA window 16:00 to 18:00
    And route "route-1" gives its stop 2 the ETA window 18:00 to 20:00

  Scenario: Windows are kept inside what is left of the wave window
    Given the packages "TRK-1", "TRK-2" and "TRK-3" are inducted in wave "wave-1" and induction is closed
    And the planner plans a route with 3 stops
    And the planner dispatches wave "wave-1"
    And the time is 16:00
    When the driver starts route "route-1"
    Then route "route-1" gives its stop 1 the ETA window 16:00 to 18:00
    And route "route-1" gives its stop 2 the ETA window 17:00 to 19:00
    And route "route-1" gives its stop 3 the ETA window 18:00 to 20:00

  Scenario: With less than two hours left the window is what is left
    Given the packages "TRK-1" are inducted in wave "wave-1" and induction is closed
    And the planner plans a route with 1 stops
    And the planner dispatches wave "wave-1"
    And the time is 18:30
    When the driver starts route "route-1"
    Then route "route-1" gives its stop 1 the ETA window 18:30 to 20:00

  Scenario: Window bounds fall on whole minutes
    Given the packages "TRK-1" are inducted in wave "wave-1" and induction is closed
    And the planner plans a route with 1 stops
    And the planner dispatches wave "wave-1"
    And the time is 18:30:30
    When the driver starts route "route-1"
    Then route "route-1" gives its stop 1 the ETA window 18:30 to 19:59

  Scenario: Exactly 30 minutes left is still enough for an ETA window
    Given the packages "TRK-1" are inducted in wave "wave-1" and induction is closed
    And the planner plans a route with 1 stops
    And the planner dispatches wave "wave-1"
    And the time is 19:30
    When the driver starts route "route-1"
    Then route "route-1" gives its stop 1 the ETA window 19:30 to 20:00

  Scenario: With less than 30 minutes left no ETA is raised and the route starts normally
    Given the packages "TRK-1" are inducted in wave "wave-1" and induction is closed
    And the planner plans a route with 1 stops
    And the planner dispatches wave "wave-1"
    And the time is 19:31
    When the driver starts route "route-1"
    Then the request is accepted
    And route "route-1" is "OUT_FOR_DELIVERY"
    And exactly 1 "PackageOutForDelivery" event was published
    And exactly 0 "StopEtaWindowAssigned" events were published

  Scenario: One stop of three packages gets one ETA window and three out-for-delivery events
    Given the label of "TRK-1" says stop "k-a"
    And the label of "TRK-2" says stop "k-a"
    And the label of "TRK-3" says stop "k-a"
    And a started route with one stop of the packages "TRK-1", "TRK-2" and "TRK-3"
    Then exactly 3 "PackageOutForDelivery" events were published
    And exactly 1 "StopEtaWindowAssigned" event was published
    And the ETA window of stop 1 of route "route-1" covers the packages "TRK-1,TRK-2,TRK-3" of the stop "k-a"
    And route "route-1" gives its stop 1 the ETA window 15:00 to 17:00

  Scenario: A re-inducted package gets a new ETA window from its next route start
    Given a wave "wave-2" with its cutoff in 48 hours and capacity 5
    And a started route "route-1" with the packages "TRK-1" and "TRK-2"
    And on route "route-1" the driver records "TRK-1" as failed because "recipient absent"
    And on route "route-1" the driver records "TRK-2" as delivered
    And induction of wave "wave-2" is closed
    And the client posts to "/routes" with a fresh key and body:
      """
      {"waveId":"wave-2","dspId":"dsp-1","stops":[{"stopNo":1,"trackingNumbers":["TRK-1"]}]}
      """
    And the client posts to "/waves/wave-2/dispatches" with a fresh key
    When the client posts to "/routes/route-2/starts" with a fresh key
    Then the response is 200
    And exactly 3 "StopEtaWindowAssigned" events were published
    And the ETA window of stop 1 of route "route-2" covers the packages "TRK-1" of the stop "pkg:TRK-1"

  # --- RecordAttempt: the attempt as the shopper's timeline will see it ---

  Scenario: A failed attempt is announced with its detail and reason
    Given a started route "route-1" with the packages "TRK-1" and "TRK-2"
    When on route "route-1" the driver records "TRK-1" as failed with the detail "ACCESS_PROBLEM" because "gate locked"
    Then the "DeliveryAttempted" event for route "route-1" and package "TRK-1" carries the outcome "FAILED", the detail "ACCESS_PROBLEM" and the reason "gate locked"

  Scenario: A delivery in a safe place is announced with its detail and no reason
    Given a started route "route-1" with the packages "TRK-1" and "TRK-2"
    When on route "route-1" the driver records "TRK-1" as delivered in a safe place
    Then the "DeliveryAttempted" event for route "route-1" and package "TRK-1" carries the outcome "DELIVERED", the detail "SAFE_PLACE_USED" and the reason ""
    And the package "TRK-1" is "DELIVERED"

  Scenario Outline: An outcome and a detail that pair are accepted
    Given a started route "route-1" with the packages "TRK-1" and "TRK-2"
    When the client posts to "/routes/route-1/attempts" with a fresh key and body:
      """
      {"trackingNumber":"TRK-1","outcome":"<outcome>","outcomeDetail":"<detail>","failureReason":"<reason>","at":"2026-10-24T14:00:00Z"}
      """
    Then the response is 201
    And the response field "outcome" is "<outcome>"
    And exactly 1 "DeliveryAttempted" event was published

    Examples:
      | outcome   | detail          | reason           |
      | FAILED    | NOT_HOME        | nobody answered  |
      | FAILED    | ACCESS_PROBLEM  | gate locked      |
      | FAILED    | OTHER           | van broke down   |
      | FAILED    |                 | nobody answered  |
      | DELIVERED | SAFE_PLACE_USED |                  |
      | DELIVERED |                 |                  |

  Scenario Outline: An outcome and a detail that do not pair, or a missing or stray reason, are refused
    Given a started route "route-1" with the packages "TRK-1" and "TRK-2"
    When the client posts to "/routes/route-1/attempts" with a fresh key and body:
      """
      {"trackingNumber":"TRK-1","outcome":"<outcome>","outcomeDetail":"<detail>","failureReason":"<reason>","at":"2026-10-24T14:00:00Z"}
      """
    Then the response is 422
    And the response problem is "validation-failed"
    And exactly 0 "DeliveryAttempted" events were published
    And the package "TRK-1" is "INDUCTED"

    Examples:
      | outcome   | detail          | reason          |
      | FAILED    | SAFE_PLACE_USED | left it         |
      | FAILED    | WEATHER         | rain            |
      | FAILED    | NOT_HOME        |                 |
      | DELIVERED | NOT_HOME        |                 |
      | DELIVERED | ACCESS_PROBLEM  |                 |
      | DELIVERED | OTHER           |                 |
      | DELIVERED |                 | left at the door |
      | LOST      |                 |                 |

  Scenario Outline: A failure reason of 1 to 200 characters is accepted, 201 is refused
    Given a started route "route-1" with the packages "TRK-1" and "TRK-2"
    When the client sends the attempt of "TRK-1" as FAILED with a reason of <length> characters to route "route-1"
    Then the response is <status>

    Examples:
      | length | status |
      | 1      | 201    |
      | 200    | 201    |
      | 201    | 422    |

  Scenario: An attempt needs the instant it happened
    Given a started route "route-1" with the packages "TRK-1" and "TRK-2"
    When the client posts to "/routes/route-1/attempts" with a fresh key and body:
      """
      {"trackingNumber":"TRK-1","outcome":"DELIVERED"}
      """
    Then the response is 422
    And the response problem is "validation-failed"
    And exactly 0 "DeliveryAttempted" events were published

  Scenario: A package that is not on the route is not found
    Given a started route "route-1" with the packages "TRK-1" and "TRK-2"
    When the client posts to "/routes/route-1/attempts" with a fresh key and body:
      """
      {"trackingNumber":"TRK-9","outcome":"DELIVERED","at":"2026-10-24T14:00:00Z"}
      """
    Then the response is 404
    And the response problem is "package-not-on-route"

  Scenario: An attempt on a route that does not exist is not found
    When the client posts to "/routes/route-404/attempts" with a fresh key and body:
      """
      {"trackingNumber":"TRK-1","outcome":"DELIVERED","at":"2026-10-24T14:00:00Z"}
      """
    Then the response is 404
    And the response problem is "not-found"

  Scenario: One attempt per package: a second after a failure is refused
    Given a started route "route-1" with the packages "TRK-1" and "TRK-2"
    And on route "route-1" the driver records "TRK-1" as failed because "nobody answered"
    When the client posts to "/routes/route-1/attempts" with a fresh key and body:
      """
      {"trackingNumber":"TRK-1","outcome":"DELIVERED","at":"2026-10-24T15:00:00Z"}
      """
    Then the response is 409
    And the response problem is "attempt-already-recorded"
    And exactly 1 "DeliveryAttempted" event was published

  Scenario: A delivered package cannot be attempted again, even under a new key
    Given a started route "route-1" with the packages "TRK-1" and "TRK-2"
    And on route "route-1" the driver records "TRK-1" as delivered
    When the client posts to "/routes/route-1/attempts" with a fresh key and body:
      """
      {"trackingNumber":"TRK-1","outcome":"DELIVERED","at":"2026-10-24T15:00:00Z"}
      """
    Then the response is 409
    And the response problem is "already-delivered"
    And exactly 1 "DeliveryAttempted" event was published

  Scenario: A completed route takes no more attempts
    Given a started route "route-1" with the packages "TRK-1" and "TRK-2"
    And on route "route-1" the driver records "TRK-1" as delivered
    And on route "route-1" the driver records "TRK-2" as delivered
    When the client posts to "/routes/route-1/attempts" with a fresh key and body:
      """
      {"trackingNumber":"TRK-1","outcome":"DELIVERED","at":"2026-10-24T15:00:00Z"}
      """
    Then the response is 409
    And the response problem is "route-not-started"

  # --- Route completion (A16) and the derived stop outcome (A17) ---

  Scenario: A route stays out for delivery while a package has no attempt
    Given a started route "route-1" with the packages "TRK-1" and "TRK-2"
    When on route "route-1" the driver records "TRK-1" as delivered
    Then route "route-1" is "OUT_FOR_DELIVERY"
    And wave "wave-1" is "DISPATCHED"

  Scenario: A failure counts as an attempt: the route and the wave complete
    Given a started route "route-1" with the packages "TRK-1" and "TRK-2"
    When on route "route-1" the driver records "TRK-1" as delivered
    And on route "route-1" the driver records "TRK-2" as failed because "nobody answered"
    Then route "route-1" is "COMPLETED"
    And wave "wave-1" is "COMPLETED"

  Scenario: A stop has no outcome until every package of it has an attempt
    Given the label of "TRK-1" says stop "k-a"
    And the label of "TRK-2" says stop "k-a"
    And a started route with one stop of the packages "TRK-1" and "TRK-2"
    When on route "route-1" the driver records "TRK-1" as delivered
    And the client requests "/routes/route-1"
    Then the response is 200
    And the response has no field "stops.0.outcome"

  Scenario: A stop whose packages were all delivered is DELIVERED
    Given the label of "TRK-1" says stop "k-a"
    And the label of "TRK-2" says stop "k-a"
    And a started route with one stop of the packages "TRK-1" and "TRK-2"
    When on route "route-1" the driver records "TRK-1" as delivered
    And on route "route-1" the driver records "TRK-2" as delivered
    And the client requests "/routes/route-1"
    Then the response field "stops.0.outcome" is "DELIVERED"
    And the response field "state" is "COMPLETED"

  Scenario: A stop with one failed package is FAILED
    Given the label of "TRK-1" says stop "k-a"
    And the label of "TRK-2" says stop "k-a"
    And a started route with one stop of the packages "TRK-1" and "TRK-2"
    When on route "route-1" the driver records "TRK-1" as delivered
    And on route "route-1" the driver records "TRK-2" as failed because "nobody answered"
    And the client requests "/routes/route-1"
    Then the response field "stops.0.outcome" is "FAILED"

  # --- RetryNextWave (A18, A19) ---

  Scenario: A second failed attempt still sends the package to the next wave
    Given a wave "wave-2" with its cutoff in 48 hours and capacity 5
    And the package "TRK-1" already failed once
    And a started route "route-1" with the packages "TRK-1" and "TRK-2"
    When on route "route-1" the driver records "TRK-1" as failed because "recipient absent"
    Then the package "TRK-1" is inducted in wave "wave-2"
    And the package "TRK-1" failed 2 times
    And the package "TRK-1" is "INDUCTED"
    And exactly 0 "PackageReturnInitiated" events were published

  Scenario: A package returned to its sender after the third failure takes no wave capacity
    Given a wave "wave-2" with its cutoff in 48 hours and capacity 5
    And the package "TRK-1" already failed twice
    And a started route "route-1" with the packages "TRK-1" and "TRK-2"
    When on route "route-1" the driver records "TRK-1" as failed because "recipient absent"
    Then the package "TRK-1" is "RETURN_INITIATED"
    And the package "TRK-1" failed 3 times
    And wave "wave-2" holds 0 packages
    And exactly 0 "PackageInductionDeferred" events were published
    And exactly 1 "PackageReturnInitiated" event was published

  Scenario: A failed package finding no next wave waits at the station and is announced
    Given a started route "route-1" with the packages "TRK-1" and "TRK-2"
    When on route "route-1" the driver records "TRK-1" as failed because "recipient absent"
    Then the package "TRK-1" waits at the station
    And the package "TRK-1" failed 1 time
    And exactly 1 "PackageInductionDeferred" event was published

  Scenario: The next wave is judged at the instant of the attempt, not at the start of the route
    Given a wave "wave-2" with its cutoff in 5 hours and capacity 5
    And a started route "route-1" with the packages "TRK-1" and "TRK-2"
    When on route "route-1" the driver records "TRK-1" as failed because "recipient absent"
    Then the package "TRK-1" waits at the station
    And wave "wave-2" holds 0 packages

  Scenario: A delivered package that arrives again is ignored
    Given a wave "wave-2" with its cutoff in 48 hours and capacity 5
    And a started route "route-1" with the packages "TRK-1" and "TRK-2"
    And on route "route-1" the driver records "TRK-1" as delivered
    When the package "TRK-1" arrives at the station
    Then the package "TRK-1" is "DELIVERED"
    And wave "wave-2" holds 0 packages

  Scenario: A package returned to its sender that arrives again is ignored
    Given a wave "wave-2" with its cutoff in 48 hours and capacity 5
    And the package "TRK-1" already failed twice
    And a started route "route-1" with the packages "TRK-1" and "TRK-2"
    And on route "route-1" the driver records "TRK-1" as failed because "recipient absent"
    When the package "TRK-1" arrives at the station
    Then the package "TRK-1" is "RETURN_INITIATED"
    And wave "wave-2" holds 0 packages
