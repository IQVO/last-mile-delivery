Feature: Delivery service partners
  Background:
    Given a delivery station "node-ds-01"

  Scenario: A partner needs a positive van capacity
    When the planner contracts a partner with a van of 0
    Then the request is refused with "invalid van capacity"

  Scenario: A partner needs a known delivery station
    When the planner contracts a partner for the station "node-unknown"
    Then the request is refused with "unknown station"

  Scenario: A deactivated partner is given no new route
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And a partner "dsp-1" with a van of 2
    And the packages "TRK-1" are inducted in wave "wave-1" and induction is closed
    And the partner "dsp-1" is deactivated
    When the planner plans a route with 1 stops
    Then the request is refused with "partner inactive"
