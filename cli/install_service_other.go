//go:build !darwin && !linux && !windows

package cli

import "errors"

var errPlatformNotSupported = errors.New(
	"outcrop install-service is supported on darwin, linux, and windows; " +
		"on other platforms run `outcrop serve` from your shell or your favourite supervisor")

func platformInstall(_ string) error              { return errPlatformNotSupported }
func platformUninstall() error                    { return errPlatformNotSupported }
func platformStatus() (bool, bool, string, error) { return false, false, "", errPlatformNotSupported }
