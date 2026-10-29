package power

import (
	"context"
	"fmt"
	"os/exec"
)

// awake reports whether the Mac is in a full wake, from IOPMrootDomain's
// System Capabilities as `ioreg -r -n IOPMrootDomain -d 1 -a` prints it
// (ADR 0047).
func awake(ctx context.Context) (bool, error) {
	out, err := exec.CommandContext(ctx, "/usr/sbin/ioreg", "-r", "-n", "IOPMrootDomain", "-d", "1", "-a").Output()
	if err != nil {
		return false, fmt.Errorf("running ioreg: %w", err)
	}
	return awakeFrom(out)
}
