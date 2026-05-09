package droplet

import (
	"testing"

	"github.com/flowcase/flowcase/internal/models"
)

func tinyDroplet(cores, memMB int) *models.Droplet {
	return &models.Droplet{
		ID:              "tiny",
		DisplayName:     "Tiny",
		DropletType:     "container",
		ContainerCores:  ptr(cores),
		ContainerMemory: ptr(memMB),
	}
}

func bigDroplet() *models.Droplet {
	return &models.Droplet{
		ID:              "big",
		DisplayName:     "Big",
		DropletType:     "container",
		ContainerCores:  ptr(64),
		ContainerMemory: ptr(64000), // 64 GiB-ish
	}
}

// 8 GB system in bytes.
func eightGBMem() (uint64, error) { return 8 * 1024 * 1024 * 1024, nil }

func TestCheckResourcesPassesWhenIdle(t *testing.T) {
	ok, msg, err := CheckResources(CheckResourcesInput{
		Droplet:       tinyDroplet(1, 256),
		Instances:     nil,
		GetDroplet:    func(id string) (*models.Droplet, error) { return nil, nil },
		MemTotalBytes: eightGBMem,
		NumCPU:        4,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Errorf("idle should pass: %s", msg)
	}
}

func TestCheckResourcesRefusesGiantDropletOnSmallHost(t *testing.T) {
	ok, msg, err := CheckResources(CheckResourcesInput{
		Droplet:       bigDroplet(),
		Instances:     nil,
		GetDroplet:    func(id string) (*models.Droplet, error) { return nil, nil },
		MemTotalBytes: eightGBMem,
		NumCPU:        4,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Error("64GB droplet should fail on 8GB host")
	}
	if msg == "" {
		t.Error("expected a refusal message")
	}
}

func TestCheckResourcesRefusesWhenCumulativeExceedsCeiling(t *testing.T) {
	// 8 GB total, 85% allowed = ~6.96 GB.
	// Already-running instance uses 6 GB; new request asks for 1 GB -> 7 GB > 6.96 GB.
	bigInstanceDroplet := tinyDroplet(2, 6_000) // 6 GB existing
	get := func(id string) (*models.Droplet, error) {
		if id == "exists" {
			return bigInstanceDroplet, nil
		}
		return nil, nil
	}
	ok, msg, err := CheckResources(CheckResourcesInput{
		Droplet:       tinyDroplet(1, 1_000), // wants another 1 GB
		Instances:     []models.DropletInstance{{ID: "i", DropletID: "exists"}},
		GetDroplet:    get,
		MemTotalBytes: eightGBMem,
		NumCPU:        4,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Errorf("should refuse — projected %d MB > 0.85 * 8192 MB; msg=%q", 6_000+1_000, msg)
	}
}

func TestCheckResourcesAllowsCPUOversubscription(t *testing.T) {
	// 4 cores * 2.0 = 8 cores allowed. Requesting 7 should pass.
	ok, msg, err := CheckResources(CheckResourcesInput{
		Droplet:       tinyDroplet(7, 100),
		Instances:     nil,
		GetDroplet:    func(id string) (*models.Droplet, error) { return nil, nil },
		MemTotalBytes: eightGBMem,
		NumCPU:        4,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Errorf("7 cores on 4 CPU should pass via 2x oversubscription; msg=%q", msg)
	}
}

func TestCheckResourcesRefusesWhenCPUExceedsOversubscription(t *testing.T) {
	// 4 cores * 2.0 = 8 cores allowed. Requesting 9 should fail.
	ok, _, err := CheckResources(CheckResourcesInput{
		Droplet:       tinyDroplet(9, 100),
		Instances:     nil,
		GetDroplet:    func(id string) (*models.Droplet, error) { return nil, nil },
		MemTotalBytes: eightGBMem,
		NumCPU:        4,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Error("9 cores on 4 CPU should fail (max 8 with 2x oversubscription)")
	}
}

func TestCheckResourcesSkipsOrphanInstances(t *testing.T) {
	// Instance points at a deleted droplet — droplet_dict.get returns
	// nil; it shouldn't contribute to the totals.
	get := func(id string) (*models.Droplet, error) { return nil, nil }
	ok, _, err := CheckResources(CheckResourcesInput{
		Droplet:       tinyDroplet(1, 100),
		Instances:     []models.DropletInstance{{ID: "i", DropletID: "ghost"}},
		GetDroplet:    get,
		MemTotalBytes: eightGBMem,
		NumCPU:        4,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Error("orphan instance should be ignored, not double-counted")
	}
}

func TestCheckResourcesRequiresDroplet(t *testing.T) {
	if _, _, err := CheckResources(CheckResourcesInput{}); err == nil {
		t.Error("expected error on nil droplet")
	}
}

func TestCheckResourcesPropagatesGetDropletErrors(t *testing.T) {
	get := func(id string) (*models.Droplet, error) {
		return nil, errInjected
	}
	_, _, err := CheckResources(CheckResourcesInput{
		Droplet:    tinyDroplet(1, 100),
		Instances:  []models.DropletInstance{{ID: "i", DropletID: "x"}},
		GetDroplet: get,
	})
	if err == nil {
		t.Error("expected GetDroplet error to propagate")
	}
}

var errInjected = injectedErr{}

type injectedErr struct{}

func (injectedErr) Error() string { return "injected" }
