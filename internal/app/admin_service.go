package app

import (
	"context"
	"runtime"

	"github.com/flowcase/flowcase/internal/domain"
	"github.com/shirou/gopsutil/v4/mem"
)

type AdminService struct {
	store domain.Store
}

func NewAdminService(store domain.Store) *AdminService {
	return &AdminService{store: store}
}

type SystemInfo struct {
	CPUCount    int    `json:"cpu_count"`
	MemoryTotal uint64 `json:"memory_total_mb"`
	MemoryUsed  uint64 `json:"memory_used_mb"`
	MemoryFree  uint64 `json:"memory_free_mb"`
	GoVersion   string `json:"go_version"`
	NumGoroutine int   `json:"goroutines"`
}

func (s *AdminService) GetSystemInfo(ctx context.Context) (*SystemInfo, error) {
	info := &SystemInfo{
		CPUCount:     runtime.NumCPU(),
		GoVersion:    runtime.Version(),
		NumGoroutine: runtime.NumGoroutine(),
	}

	if v, err := mem.VirtualMemory(); err == nil {
		info.MemoryTotal = v.Total / 1024 / 1024
		info.MemoryUsed = v.Used / 1024 / 1024
		info.MemoryFree = v.Available / 1024 / 1024
	}

	return info, nil
}

func (s *AdminService) ListLogs(ctx context.Context, limit int) ([]domain.LogEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	return s.store.ListLogs(ctx, limit)
}

func (s *AdminService) CreateLog(ctx context.Context, level, message string) error {
	return s.store.CreateLog(ctx, &domain.LogEntry{
		Level:   level,
		Message: message,
	})
}

func (s *AdminService) ListRegistries(ctx context.Context) ([]domain.Registry, error) {
	return s.store.ListRegistries(ctx)
}

func (s *AdminService) CreateRegistry(ctx context.Context, url string) (*domain.Registry, error) {
	r := &domain.Registry{URL: url}
	if err := s.store.CreateRegistry(ctx, r); err != nil {
		return nil, err
	}
	return r, nil
}
