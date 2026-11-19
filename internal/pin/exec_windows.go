//go:build windows

package pin

import (
	"errors"
	"os"
	"os/exec"
)

// Exec runs binary as a child with inherited stdio and returns its exit
// status: Windows has no exec(2).
func Exec(binary string, args []string, pinned string) (int, error) {
	cmd := exec.Command(binary, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), ActiveEnv+"="+pinned)
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	if err != nil {
		return 0, err
	}
	return 0, nil
}
