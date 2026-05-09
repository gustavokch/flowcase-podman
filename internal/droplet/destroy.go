package droplet

import (
	"context"
	"regexp"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/errdefs"

	"github.com/flowcase/flowcase/internal/dockerx"
	"github.com/flowcase/flowcase/internal/log"
)

// Destroy force-removes the session container for `instanceID`.
// Not-found errors are silenced — the cleanup path doesn't care
// whether the container disappeared on its own or via this call.
//
// Mirrors the destroy paths at routes/droplet.py:684-695 and
// routes/auth.py:144-152 (logout-time teardown).
func Destroy(ctx context.Context, dx *dockerx.Client, instanceID string) error {
	name := ContainerNamePrefix + instanceID
	err := dx.Raw().ContainerRemove(ctx, name, container.RemoveOptions{Force: true})
	if err == nil {
		log.Info("Removed container %s", name)
		return nil
	}
	if errdefs.IsNotFound(err) {
		return nil
	}
	return err
}

// orphanRegex matches the legacy `flowcase_generated_<uuid>` naming.
// Mirrors utils/docker.py:73 — the dashed-segments pattern is used
// instead of a strict UUIDv4 regex because nothing in the orchestrator
// guarantees v4. Case-insensitive to match the (?i) flag in Python.
var orphanRegex = regexp.MustCompile(`(?i)^flowcase_generated_([a-z0-9]+(?:-[a-z0-9]+)+)$`)

// CleanupReport summarizes a CleanupOrphans run for callers that want
// to surface the numbers (e.g. startup logs, admin UI).
type CleanupReport struct {
	Found     int
	Removed   int
	Restarted int
}

// CleanupOrphans reconciles orchestrator-spawned containers against the
// list of instance IDs the DB still has. For each container whose name
// starts with `flowcase_generated_`:
//   - if the instance ID is NOT in `knownInstanceIDs` -> stop + remove
//   - if it IS known but the container isn't running -> restart it
//
// Mirrors utils/docker.py:42-113. The legacy "no app context" branch
// (where unknown is treated as known) is dropped: callers always know
// what's in the DB, so passing nil is unsupported. Pass an empty slice
// to mean "the DB is empty, remove every orchestrator container".
func CleanupOrphans(ctx context.Context, dx *dockerx.Client, knownInstanceIDs []string) (CleanupReport, error) {
	known := make(map[string]struct{}, len(knownInstanceIDs))
	for _, id := range knownInstanceIDs {
		known[id] = struct{}{}
	}

	all, err := dx.Raw().ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return CleanupReport{}, err
	}

	var report CleanupReport
	for _, c := range all {
		name := primaryName(c.Names)
		instanceID, ok := matchOrphan(name)
		if !ok {
			continue
		}
		report.Found++

		if _, isKnown := known[instanceID]; !isKnown {
			report.Removed++
			log.Info("Removing orphaned container %s (status: %s)", name, c.State)
			// container.RemoveOptions{Force: true} = stop + remove
			// in one call; the legacy code calls .stop() + .remove()
			// separately but the effect is the same and tolerates
			// already-stopped containers.
			if err := dx.Raw().ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true}); err != nil {
				log.Error("Error removing container %s: %s", name, err)
			}
			continue
		}

		if c.State != "running" {
			report.Restarted++
			log.Info("Restarting container %s (status: %s)", name, c.State)
			if err := dx.Raw().ContainerRestart(ctx, c.ID, container.StopOptions{}); err != nil {
				log.Error("Error restarting container %s: %s", name, err)
			}
		}
	}

	log.Info(
		"Container cleanup complete: %d flowcase containers found, %d orphaned containers removed, %d containers restarted",
		report.Found, report.Removed, report.Restarted,
	)
	return report, nil
}

// primaryName returns the first non-empty name from a docker container
// list entry, with the leading slash trimmed.
func primaryName(names []string) string {
	for _, n := range names {
		n = strings.TrimPrefix(n, "/")
		if n != "" {
			return n
		}
	}
	return ""
}

// matchOrphan returns the instance ID portion of a flowcase-generated
// container name, plus a match flag. matchOrphan("foo") -> "", false.
func matchOrphan(name string) (string, bool) {
	m := orphanRegex.FindStringSubmatch(name)
	if m == nil {
		return "", false
	}
	return m[1], true
}
