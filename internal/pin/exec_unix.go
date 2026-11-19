//go:build !windows

package pin

import (
	"os"
	"syscall"
)

// Exec replaces this process with binary, keeping the pid, so signals and
// the exit status pass straight through. It returns only on failure.
func Exec(binary string, args []string, pinned string) (int, error) {
	env := append(os.Environ(), ActiveEnv+"="+pinned)
	argv := append([]string{binary}, args...)
	return 0, syscall.Exec(binary, argv, env)
}
