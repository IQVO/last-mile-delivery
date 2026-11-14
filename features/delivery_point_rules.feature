Feature: Delivery point profile rules taken from the documentation
  Scenarios derived from the aggregate design canvas (DeliveryPointProfile), the
  domain events (DeliveryPointFlagged) and ADR 0014: two counted failures inside
  60 days flag a delivery point, one strike per route visit, a delivery ends the
  episode, the flag changes nothing else. The packages of one stop share a stop
  key from their label; each failed visit comes from a different route.

  Background:
    Given a delivery station "node-ds-01"
    And a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And a partner "dsp-1" with a van of 3

  Scenario: Two NOT_HOME failures at one point on two routes flag the point
    Given the packages "TRK-1" and "TRK-2" of the stop "k-a" each ride their own started route
    When on route "route-1" the driver records "TRK-1" as failed with the detail "NOT_HOME" because "nobody answered"
    Then exactly 0 "DeliveryPointFlagged" events were published
    And the delivery point "k-a" has 1 open strike
    And the delivery point "k-a" is not flagged
    When on route "route-2" the driver records "TRK-2" as failed with the detail "NOT_HOME" because "nobody answered"
    Then exactly 1 "DeliveryPointFlagged" event was published
    And the latest "DeliveryPointFlagged" event for the stop "k-a" flags 2 failures for the packages "TRK-1,TRK-2" because NOT_HOME
    And the delivery point "k-a" is flagged

  Scenario Outline: The flag's reason is ACCESS_PROBLEM when any open strike has it
    Given the packages "TRK-1" and "TRK-2" of the stop "k-a" each ride their own started route
    When on route "route-1" the driver records "TRK-1" as failed with the detail "<first>" because "no luck"
    And on route "route-2" the driver records "TRK-2" as failed with the detail "<second>" because "no luck"
    Then the latest "DeliveryPointFlagged" event for the stop "k-a" flags 2 failures for the packages "TRK-1,TRK-2" because <reason>

    Examples:
      | first          | second         | reason         |
      | NOT_HOME       | ACCESS_PROBLEM | ACCESS_PROBLEM |
      | ACCESS_PROBLEM | NOT_HOME       | ACCESS_PROBLEM |
      | ACCESS_PROBLEM | ACCESS_PROBLEM | ACCESS_PROBLEM |

  Scenario Outline: OTHER and not-reported failures are tallied but never counted as strikes
    Given the packages "TRK-1" and "TRK-2" of the stop "k-a" each ride their own started route
    When on route "route-1" the driver records "TRK-1" as failed with the detail "<first>" because "no luck"
    And on route "route-2" the driver records "TRK-2" as failed with the detail "<second>" because "no luck"
    Then exactly 0 "DeliveryPointFlagged" events were published
    And the delivery point "k-a" is not flagged
    And the delivery point "k-a" has <strikes> open strike

    Examples:
      | first    | second | strikes |
      | NOT_HOME | OTHER  | 1       |
      | OTHER    | OTHER  | 0       |
      | NOT_HOME |        | 1       |
      |          |        | 0       |

  Scenario: Until the driver reports a detail every failure is tallied as other and nothing is flagged
    Given the packages "TRK-1" and "TRK-2" of the stop "k-a" each ride their own started route
    When on route "route-1" the driver records "TRK-1" as failed because "nobody answered"
    And on route "route-2" the driver records "TRK-2" as failed because "nobody answered"
    Then exactly 0 "DeliveryPointFlagged" events were published
    And the delivery point "k-a" tallied 2 attempts: 0 delivered (0 in a safe place), 0 not home, 0 access problem, 2 other

  Scenario: Two failed packages on one route are one failed visit, one strike
    Given the label of "TRK-1" says stop "k-a"
    And the label of "TRK-2" says stop "k-a"
    And a started route with one stop of the packages "TRK-1" and "TRK-2"
    When on route "route-1" the driver records "TRK-1" as failed with the detail "NOT_HOME" because "nobody answered"
    And on route "route-1" the driver records "TRK-2" as failed with the detail "NOT_HOME" because "nobody answered"
    Then exactly 0 "DeliveryPointFlagged" events were published
    And the delivery point "k-a" has 1 open strike
    And the delivery point "k-a" tallied 2 attempts: 0 delivered (0 in a safe place), 2 not home, 0 access problem, 0 other

  Scenario: A delivery ends the episode, so the next failure is a first strike again
    Given the packages "TRK-1", "TRK-2" and "TRK-3" of the stop "k-a" each ride their own started route
    When on route "route-1" the driver records "TRK-1" as failed with the detail "NOT_HOME" because "nobody answered"
    And on route "route-2" the driver records "TRK-2" as delivered
    And on route "route-3" the driver records "TRK-3" as failed with the detail "NOT_HOME" because "nobody answered"
    Then exactly 0 "DeliveryPointFlagged" events were published
    And the delivery point "k-a" has 1 open strike
    And the delivery point "k-a" is not flagged

  Scenario: A delivery on a route that already struck the point does not end the episode
    Given the packages "TRK-1" and "TRK-2" of the stop "k-a" ride one started route and the package "TRK-3" of the same stop rides another
    When on route "route-1" the driver records "TRK-1" as failed with the detail "NOT_HOME" because "nobody answered"
    And on route "route-1" the driver records "TRK-2" as delivered
    And on route "route-2" the driver records "TRK-3" as failed with the detail "NOT_HOME" because "nobody answered"
    Then the latest "DeliveryPointFlagged" event for the stop "k-a" flags 2 failures for the packages "TRK-1,TRK-3" because NOT_HOME
    And the delivery point "k-a" is flagged

  Scenario Outline: A strike counts while it is at most 60 days older than the newest attempt
    Given the packages "TRK-1" and "TRK-2" of the stop "k-a" each ride their own started route
    When on route "route-1" the driver records "TRK-1" as failed with the detail "NOT_HOME" because "nobody answered"
    And on route "route-2" the driver records "TRK-2" as failed with the detail "NOT_HOME" because "nobody answered" on day <days>
    Then exactly <events> "DeliveryPointFlagged" events were published
    And the delivery point "k-a" is <state>
    And the delivery point "k-a" has <strikes> open strikes

    Examples:
      | days | events | state       | strikes |
      | 59   | 1      | flagged     | 2       |
      | 60   | 1      | flagged     | 2       |
      | 61   | 0      | not flagged | 1       |

  Scenario: The flag clears when old strikes decay at the next observation
    Given the packages "TRK-1", "TRK-2" and "TRK-3" of the stop "k-a" each ride their own started route
    And on route "route-1" the driver records "TRK-1" as failed with the detail "NOT_HOME" because "nobody answered"
    And on route "route-2" the driver records "TRK-2" as failed with the detail "NOT_HOME" because "nobody answered"
    And the delivery point "k-a" is flagged
    When on route "route-3" the driver records "TRK-3" as failed with the detail "OTHER" because "road closed" on day 61
    Then the delivery point "k-a" is not flagged
    And the delivery point "k-a" has 0 open strikes
    And exactly 1 "DeliveryPointFlagged" event was published

  Scenario: A later parcel failing at an already flagged point is told once, and the flag keeps its first instant
    Given the packages "TRK-1", "TRK-2" and "TRK-3" of the stop "k-a" each ride their own started route
    When on route "route-1" the driver records "TRK-1" as failed with the detail "NOT_HOME" because "nobody answered"
    And on route "route-2" the driver records "TRK-2" as failed with the detail "NOT_HOME" because "nobody answered"
    And on route "route-3" the driver records "TRK-3" as failed with the detail "NOT_HOME" because "nobody answered" on day 1
    Then exactly 2 "DeliveryPointFlagged" events were published
    And the latest "DeliveryPointFlagged" event for the stop "k-a" flags 3 failures for the packages "TRK-3" because NOT_HOME
    And the delivery point "k-a" was flagged on day 0

  Scenario: A package already told about is not told again when it fails a third time at the flagged point
    Given a wave "wave-2" with its cutoff in 48 hours and capacity 5
    And a wave "wave-3" with its cutoff in 72 hours and capacity 5
    And the label of "TRK-1" says stop "k-a"
    And a started route with one stop of the packages "TRK-1"
    And on route "route-1" the driver records "TRK-1" as failed with the detail "NOT_HOME" because "nobody answered"
    And induction of wave "wave-2" is closed
    And the client posts to "/routes" with a fresh key and body:
      """
      {"waveId":"wave-2","dspId":"dsp-1","stops":[{"stopNo":1,"trackingNumbers":["TRK-1"]}]}
      """
    And the planner dispatches wave "wave-2"
    And the driver starts route "route-2"
    And on route "route-2" the driver records "TRK-1" as failed with the detail "NOT_HOME" because "nobody answered"
    And induction of wave "wave-3" is closed
    And the client posts to "/routes" with a fresh key and body:
      """
      {"waveId":"wave-3","dspId":"dsp-1","stops":[{"stopNo":1,"trackingNumbers":["TRK-1"]}]}
      """
    And the planner dispatches wave "wave-3"
    And the driver starts route "route-3"
    When on route "route-3" the driver records "TRK-1" as failed with the detail "NOT_HOME" because "nobody answered"
    Then the request is accepted
    And exactly 1 "DeliveryPointFlagged" event was published
    And the latest "DeliveryPointFlagged" event for the stop "k-a" flags 2 failures for the packages "TRK-1" because NOT_HOME
    And the delivery point "k-a" has 3 open strikes

  Scenario: The ring keeps the five newest strikes
    Given the packages "TRK-1", "TRK-2", "TRK-3", "TRK-4", "TRK-5" and "TRK-6" of the stop "k-a" each ride their own started route
    When on route "route-1" the driver records "TRK-1" as failed with the detail "NOT_HOME" because "nobody answered"
    And on route "route-2" the driver records "TRK-2" as failed with the detail "NOT_HOME" because "nobody answered"
    And on route "route-3" the driver records "TRK-3" as failed with the detail "NOT_HOME" because "nobody answered"
    And on route "route-4" the driver records "TRK-4" as failed with the detail "NOT_HOME" because "nobody answered"
    And on route "route-5" the driver records "TRK-5" as failed with the detail "NOT_HOME" because "nobody answered"
    And on route "route-6" the driver records "TRK-6" as failed with the detail "NOT_HOME" because "nobody answered"
    Then the delivery point "k-a" has 5 open strikes
    And the latest "DeliveryPointFlagged" event for the stop "k-a" flags 5 failures for the packages "TRK-6" because NOT_HOME

  Scenario: The profile tallies every outcome, OTHER and not reported together
    Given the packages "TRK-1", "TRK-2", "TRK-3", "TRK-4" and "TRK-5" of the stop "k-a" each ride their own started route
    When on route "route-1" the driver records "TRK-1" as delivered in a safe place
    And on route "route-2" the driver records "TRK-2" as failed with the detail "NOT_HOME" because "nobody answered"
    And on route "route-3" the driver records "TRK-3" as failed with the detail "ACCESS_PROBLEM" because "gate locked"
    And on route "route-4" the driver records "TRK-4" as failed with the detail "OTHER" because "road closed"
    And on route "route-5" the driver records "TRK-5" as failed because "unknown"
    Then the delivery point "k-a" tallied 5 attempts: 1 delivered (1 in a safe place), 1 not home, 1 access problem, 2 other
    And the delivery point "k-a" is flagged
    And exactly 1 "DeliveryPointFlagged" event was published

  Scenario: A package with no label data has no delivery point profile
    Given a started route "route-1" with the packages "TRK-1" and "TRK-2"
    When on route "route-1" the driver records "TRK-1" as failed with the detail "NOT_HOME" because "nobody answered"
    And on route "route-1" the driver records "TRK-2" as failed with the detail "NOT_HOME" because "nobody answered"
    Then no delivery point profile exists
    And exactly 0 "DeliveryPointFlagged" events were published

  Scenario: A flagged point changes no route: the next route to it is planned as usual
    Given a wave "wave-2" with its cutoff in 48 hours and capacity 5
    And the packages "TRK-1" and "TRK-2" of the stop "k-a" each ride their own started route
    And on route "route-1" the driver records "TRK-1" as failed with the detail "NOT_HOME" because "nobody answered"
    And on route "route-2" the driver records "TRK-2" as failed with the detail "NOT_HOME" because "nobody answered"
    And the delivery point "k-a" is flagged
    And induction of wave "wave-2" is closed
    When the client posts to "/routes" with a fresh key and body:
      """
      {"waveId":"wave-2","dspId":"dsp-1","stops":[{"stopNo":1,"trackingNumbers":["TRK-1","TRK-2"]}]}
      """
    Then the response is 201
    And the response field "stopCount" is "1"
