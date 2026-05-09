package droplet

import (
	"errors"
	"fmt"
	"runtime"

	"github.com/shirou/gopsutil/v3/mem"

	"github.com/flowcase/flowcase/internal/log"
	"github.com/flowcase/flowcase/internal/models"
)

// Constants pulled out so tests can reason about them.
const (
	// CPUOversubscription matches max_allowed_cores = system_cores * 2.0
	// at routes/droplet.py:506. Containers share CPU via CPU shares so
	// a 2x oversubscription is the legacy compromise.
	CPUOversubscription = 2.0

	// MemorySafetyFraction matches max_allowed_memory = total * 0.85
	// at routes/droplet.py:505. The remaining 15% is reserved for
	// docker, nginx, the orchestrator itself, and OS overhead.
	MemorySafetyFraction = 0.85
)

// MemTotalFn is the platform-memory hook so tests can stub a tiny
// machine without touching gopsutil. nil means "use the real call".
type MemTotalFn func() (uint64, error)

// CheckResourcesInput carries the pieces CheckResources needs without
// making the function depend on a global *sqlx.DB.
type CheckResourcesInput struct {
	// Droplet whose start the caller is gating.
	Droplet *models.Droplet
	// All currently-running session instances. Caller queries the DB
	// once and passes them in.
	Instances []models.DropletInstance
	// Fetcher for the droplet associated with each instance.
	// Returning nil means "instance is orphaned"; matches the
	// `droplet_dict.get(...)` fall-through at droplet.py:489-490.
	GetDroplet func(id string) (*models.Droplet, error)

	// Optional injection points for tests.
	MemTotalBytes MemTotalFn // nil -> real gopsutil call
	NumCPU        int        // 0 -> runtime.NumCPU()
}

// CheckResources reports whether `in.Droplet` can be added without
// exceeding the orchestrator's CPU/memory ceilings. Returns
// (false, msg) when the request would push us over and (true, "")
// when there's headroom (or when the droplet asks for nothing).
//
// Mirrors routes/droplet.py:478-516. Memory is in MB on the droplet
// records; compared against psutil-equivalent bytes-to-MB.
func CheckResources(in CheckResourcesInput) (bool, string, error) {
	if in.Droplet == nil {
		return false, "", errors.New("CheckResources: Droplet required")
	}

	totalCores := 0
	totalMemoryMB := 0
	for _, inst := range in.Instances {
		d, err := in.GetDroplet(inst.DropletID)
		if err != nil {
			return false, "", fmt.Errorf("loading droplet %s: %w", inst.DropletID, err)
		}
		if d == nil {
			continue // orphan instance — skip, matches Python fall-through
		}
		if d.ContainerCores != nil {
			totalCores += *d.ContainerCores
		}
		if d.ContainerMemory != nil {
			totalMemoryMB += *d.ContainerMemory
		}
	}

	dropletCores := 0
	dropletMemMB := 0
	if in.Droplet.ContainerCores != nil {
		dropletCores = *in.Droplet.ContainerCores
	}
	if in.Droplet.ContainerMemory != nil {
		dropletMemMB = *in.Droplet.ContainerMemory
	}

	memBytes, err := memTotal(in.MemTotalBytes)
	if err != nil {
		return false, "", fmt.Errorf("reading system memory: %w", err)
	}
	systemTotalMB := float64(memBytes) / 1024.0 / 1024.0
	maxMem := systemTotalMB * MemorySafetyFraction

	systemCores := in.NumCPU
	if systemCores <= 0 {
		systemCores = runtime.NumCPU()
	}
	maxCores := float64(systemCores) * CPUOversubscription

	projectedMem := float64(totalMemoryMB + dropletMemMB)
	projectedCores := float64(totalCores + dropletCores)

	if projectedMem > maxMem {
		log.Error(
			"Insufficient memory to request droplet %s - would use %.0fMB of %.0fMB allowed",
			in.Droplet.DisplayName, projectedMem, maxMem,
		)
		return false, "Insufficient memory to start this droplet", nil
	}
	if projectedCores > maxCores {
		log.Error(
			"Insufficient CPU cores to request droplet %s - would use %.0f of %.0f cores allowed",
			in.Droplet.DisplayName, projectedCores, maxCores,
		)
		return false, "Insufficient CPU cores to start this droplet", nil
	}
	return true, "", nil
}

func memTotal(fn MemTotalFn) (uint64, error) {
	if fn != nil {
		return fn()
	}
	v, err := mem.VirtualMemory()
	if err != nil {
		return 0, err
	}
	return v.Total, nil
}
