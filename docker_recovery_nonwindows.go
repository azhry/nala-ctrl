//go:build !windows

package main

import "context"

func repairKnownDockerSecretsSocket(context.Context, string) (bool, error) {
	return false, nil
}
