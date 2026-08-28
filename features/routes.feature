Feature: Composing a project route
  Keel composes a stack from recipes and validates it before it builds a thing.

  Scenario: The headline Laravel route resolves in order
    Given the built-in recipe catalogue
    When I select "laravel, filament, ddev, mysql, redis, pest"
    Then the plan is valid
    And the framework is "laravel"
    And the first recipe is "laravel"

  Scenario: Filament without a database is rejected
    Given the built-in recipe catalogue
    When I select "laravel, filament, ddev"
    Then the plan is rejected because "filament requires"

  Scenario: Sail cannot be used with Magento
    Given the built-in recipe catalogue
    When I select "magento, sail"
    Then the plan is rejected because "does not apply to framework magento"
