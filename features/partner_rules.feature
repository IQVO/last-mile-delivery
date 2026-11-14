Feature: Delivery service partner rules taken from the documentation
  Scenarios derived from the use cases (ContractPartner), the aggregate design
  canvas (DeliveryServicePartner) and ADR 0002 (A1, A25): the van capacity
  boundary, a contracted partner raises no event, and deactivation.

  Background:
    Given a delivery station "node-ds-01"

  Scenario Outline: A van capacity from 1 to 2147483647 contracts an ACTIVE partner and raises no event
    When the client posts to "/partners" with a fresh key and body:
      """
      {"stationNodeId":"node-ds-01","vanCapacity":<capacity>}
      """
    Then the response is 201
    And the response field "state" is "ACTIVE"
    And the response field "dspId" is "dsp-1"
    And no event was published

    Examples:
      | capacity   |
      | 1          |
      | 2147483647 |

  Scenario Outline: A van capacity outside 1 to 2147483647 is refused
    When the client posts to "/partners" with a fresh key and body:
      """
      {"stationNodeId":"node-ds-01","vanCapacity":<capacity>}
      """
    Then the response is 422
    And the response problem is "invalid-van-capacity"

    Examples:
      | capacity   |
      | 0          |
      | -1         |
      | 2147483648 |

  Scenario: A partner can only be contracted to a known delivery station
    When the client posts to "/partners" with a fresh key and body:
      """
      {"stationNodeId":"node-ds-77","vanCapacity":3}
      """
    Then the response is 422
    And the response problem is "unknown-station"

  Scenario: A deactivated partner reads back INACTIVE and cannot be deactivated again
    Given a partner "dsp-1" with a van of 2
    When the client posts to "/partners/dsp-1/deactivations" with a fresh key
    Then the response is 200
    And the response field "state" is "INACTIVE"
    When the client posts to "/partners/dsp-1/deactivations" with a fresh key
    Then the response is 409
    And the response problem is "illegal-transition"

  Scenario: A partner that does not exist is not found
    When the client posts to "/partners/dsp-404/deactivations" with a fresh key
    Then the response is 404
    And the response problem is "not-found"
