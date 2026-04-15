package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/docker/docker/api/types/image"
)

type ImageStatus struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Exists      bool   `json:"exists"`
	FullImage   string `json:"full_image"`
}

func (e *Engine) ImageExists(ctx context.Context, fullImage string) bool {
	images, err := e.cli.ImageList(ctx, image.ListOptions{})
	if err != nil {
		return false
	}
	for _, img := range images {
		for _, tag := range img.RepoTags {
			if tag == fullImage {
				return true
			}
		}
	}
	return false
}

func (e *Engine) PullImage(ctx context.Context, fullImage string) error {
	repo, tag := splitImageTag(fullImage)

	slog.Info("pulling image", "image", fullImage)
	reader, err := e.cli.ImagePull(ctx, repo+":"+tag, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pull %s: %w", fullImage, err)
	}
	defer reader.Close()

	// Drain the pull output
	decoder := json.NewDecoder(reader)
	for {
		var msg map[string]interface{}
		if err := decoder.Decode(&msg); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
	}

	slog.Info("pulled image", "image", fullImage)
	return nil
}

func (e *Engine) GetImagesStatus(ctx context.Context, required []RequiredImage) map[string]ImageStatus {
	images, err := e.cli.ImageList(ctx, image.ListOptions{})
	if err != nil {
		slog.Error("list images failed", "error", err)
		return nil
	}

	localTags := make(map[string]bool)
	for _, img := range images {
		for _, tag := range img.RepoTags {
			localTags[tag] = true
		}
	}

	status := make(map[string]ImageStatus)
	for _, req := range required {
		status[req.ID] = ImageStatus{
			Name:        req.Name,
			Description: req.Description,
			Exists:      localTags[req.FullImage],
			FullImage:   req.FullImage,
		}
	}
	return status
}

type RequiredImage struct {
	ID          string
	Name        string
	Description string
	FullImage   string
}

// BackgroundPuller periodically pulls required images
func (e *Engine) BackgroundPuller(ctx context.Context, getRequired func() []RequiredImage, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			required := getRequired()
			for _, req := range required {
				if !e.ImageExists(ctx, req.FullImage) {
					if err := e.PullImage(ctx, req.FullImage); err != nil {
						slog.Error("background pull failed", "image", req.FullImage, "error", err)
					}
				}
			}
		}
	}
}

func splitImageTag(fullImage string) (string, string) {
	if i := strings.LastIndex(fullImage, ":"); i > 0 {
		return fullImage[:i], fullImage[i+1:]
	}
	return fullImage, "latest"
}
