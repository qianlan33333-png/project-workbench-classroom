package operations

import (
	"context"
	"errors"
	"os/exec"
)

// This source adapter uses Unix process groups and SSH. It is not used by the
// local classroom. Do not start it on a platform that cannot drain its group.
func runSourceProcess(context.Context, *exec.Cmd) error {
	return errors.New("original operations source requires a Unix worker")
}
