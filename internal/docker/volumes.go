package docker

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// RemoveProjectVolumes deletes volumes whose names match ^<project>_.
// Compose may re-adopt pre-existing volumes as external; in that case
// `compose down --volumes` skips them even though they belong to the
// instance. remove uses this as a second pass after compose down.
func RemoveProjectVolumes(ctx context.Context, project string) error {
	project = strings.TrimSpace(project)
	if project == "" {
		return nil
	}
	filter := "name=^" + project + "_"
	ls := exec.CommandContext(ctx, "docker", "volume", "ls", "-q", "--filter", filter)
	out, err := ls.Output()
	if err != nil {
		return fmt.Errorf("docker volume ls: %w", err)
	}
	names := strings.Fields(strings.TrimSpace(string(out)))
	if len(names) == 0 {
		return nil
	}
	args := append([]string{"volume", "rm", "-f"}, names...)
	if err := exec.CommandContext(ctx, "docker", args...).Run(); err != nil {
		return fmt.Errorf("docker volume rm: %w", err)
	}
	return nil
}
