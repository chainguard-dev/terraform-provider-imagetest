package main

import "fmt"

var (
	ErrTenancyNotShared     = fmt.Errorf("instance tenancy is not shared")
	ErrReservedInstanceType = fmt.Errorf("instance type requires a reservation")
	ErrNotLinux             = fmt.Errorf("instance OS type is not linux")
	ErrHasPreinstalledSW    = fmt.Errorf("instance has preinstalled software (likely as-a-service offering)")
)

func NewExtractError(key, at string, wrap error) error {
	return fmt.Errorf("failed to extract key [%s] @ [%s]: %w", key, at, wrap)
}
