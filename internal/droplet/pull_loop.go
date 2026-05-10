package droplet

import (
	"context"
	"strings"
	"time"

	"github.com/flowcase/flowcase/internal/dockerx"
	"github.com/flowcase/flowcase/internal/log"
	"github.com/flowcase/flowcase/internal/models"
)

// DefaultPullInterval matches the legacy `time.sleep(60)` cadence at
// gunicorn.conf.py:56. The orchestrator is expected to call
// RunPullLoop with this; tests override.
const DefaultPullInterval = 60 * time.Second

// PullJob describes one image to pull during a cycle. Description is
// the human-readable label that ends up in the orchestrator log
// alongside each pull, matching the legacy "({description})" suffix
// at utils/docker.py:220.
type PullJob struct {
	Registry    string
	Image       string
	Description string
}

// EnumeratePullJobs builds the list of pulls for one cycle: the
// guacamole image first, then every droplet that has a
// container_docker_image set. Mirrors the loop at
// utils/docker.py:188-213.
//
// `guacRef` is the full reference to the guac image (e.g.
// `flowcaseweb/flowcase-guac:develop`); registry stays empty for the
// guac entry to mirror the legacy code path which doesn't prepend a
// registry for it.
func EnumeratePullJobs(droplets *models.DropletsRepo, guacRef string) ([]PullJob, error) {
	jobs := []PullJob{
		{Registry: "", Image: guacRef, Description: "Guacamole VNC Server"},
	}

	all, err := droplets.List()
	if err != nil {
		return nil, err
	}
	for _, d := range all {
		if d.ContainerDockerImage == nil || *d.ContainerDockerImage == "" {
			continue
		}
		registry := ""
		if d.ContainerDockerRegistry != nil {
			registry = *d.ContainerDockerRegistry
		}
		jobs = append(jobs, PullJob{
			Registry:    registry,
			Image:       *d.ContainerDockerImage,
			Description: "Droplet: " + d.DisplayName,
		})
	}
	return jobs, nil
}

// RunPullCycle executes one full pass: enumerate jobs, pull each,
// log outcomes. Best-effort — individual pull failures are logged
// at ERROR but do NOT abort the cycle. Returns the cycle-level
// error only when EnumeratePullJobs itself fails (e.g. DB issue).
//
// `dx == nil` is a no-op with a single warning log, matching the
// legacy `if not docker_client: print("...skipping image pull")`
// at utils/docker.py:181-183.
//
// `ctx` is forwarded to the pull request; cancelling it interrupts
// the in-flight pull and the rest of the cycle.
func RunPullCycle(ctx context.Context, dx *dockerx.Client, droplets *models.DropletsRepo, guacRef string) error {
	if dx == nil {
		log.Info("No Docker client available, skipping image pull")
		return nil
	}

	jobs, err := EnumeratePullJobs(droplets, guacRef)
	if err != nil {
		log.Error("Error in pull_images: %s", err)
		return err
	}

	for _, job := range jobs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		fullName := pullJobFullRef(job)
		log.Info("Pulling required Docker image %s (%s)", fullName, job.Description)
		if err := dx.PullImage(ctx, job.Registry, job.Image); err != nil {
			log.Error("Error pulling required Docker image %s (%s): %s", fullName, job.Description, err)
			continue
		}
		log.Info("Successfully pulled required Docker image %s (%s)", fullName, job.Description)
	}

	log.Info("Required image pull for Flowcase completed")
	return nil
}

// pullJobFullRef renders the <registry>/<image> reference used in
// log lines, matching the legacy f-string at utils/docker.py:204-208.
// docker.io / empty registry collapses to just the image (mirrors
// dockerx.PullImage's actual pull behavior).
func pullJobFullRef(job PullJob) string {
	reg := strings.TrimRight(job.Registry, "/")
	if reg == "" || strings.Contains(reg, "docker.io") {
		return job.Image
	}
	return reg + "/" + job.Image
}

// RunPullLoop runs RunPullCycle in a loop, sleeping `interval`
// between cycles. Returns when ctx is cancelled. Mirrors the
// `pull_images_worker` daemon thread at gunicorn.conf.py:53-63.
//
// Order matters: legacy code sleeps FIRST and then pulls, so the
// startup foreground pull (utils/docker.py: pull_images called from
// on_starting) handled the cold-start case. We mirror that here —
// callers that want an immediate pull on boot should call
// RunPullCycle synchronously before kicking off this loop, which
// matches the gunicorn.conf.py boot sequence (line 41 init_docker,
// line 50 cleanup_containers, line 62 thread.start with the
// sleep-first body).
//
// Per-cycle errors are swallowed (legacy bare except at line 59);
// only ctx cancellation breaks the loop.
func RunPullLoop(ctx context.Context, dx *dockerx.Client, droplets *models.DropletsRepo, guacRef string, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultPullInterval
	}

	timer := time.NewTimer(interval)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		if err := RunPullCycle(ctx, dx, droplets, guacRef); err != nil {
			// Fatal-cycle errors (DB failure) get logged but the
			// loop keeps running so a transient hiccup doesn't take
			// down image freshness for the rest of the orchestrator's
			// lifetime. Matches gunicorn.conf.py:59-60.
			log.Error("Error in pull_images_worker: %s", err)
		}

		// Reset timer for next cycle. timer.Reset semantics require
		// the timer to be stopped or expired; both are true at this
		// point (we just received from C).
		timer.Reset(interval)
	}
}
