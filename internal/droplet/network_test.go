package droplet

import (
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/network"

	"github.com/flowcase/flowcase/internal/dockerx"
	"github.com/flowcase/flowcase/internal/models"
)

func makeInsp(nets map[string]string) types.ContainerJSON {
	endpoints := make(map[string]*network.EndpointSettings, len(nets))
	for name, ip := range nets {
		endpoints[name] = &network.EndpointSettings{IPAddress: ip}
	}
	return types.ContainerJSON{
		NetworkSettings: &types.NetworkSettings{
			Networks: endpoints,
		},
	}
}

func TestGetContainerIPPrefersDefaultNetwork(t *testing.T) {
	insp := makeInsp(map[string]string{
		dockerx.DefaultNetwork: "172.20.0.5",
		"some-other":           "10.0.0.5",
	})
	d := &models.Droplet{ContainerNetwork: ptr("some-other")}
	if got := GetContainerIP(insp, d); got != "172.20.0.5" {
		t.Errorf("got %q, want 172.20.0.5 (default network should win)", got)
	}
}

func TestGetContainerIPFallsBackToDropletNetwork(t *testing.T) {
	insp := makeInsp(map[string]string{
		dockerx.DefaultNetwork: "", // attached but no IP
		"droplet-net":          "10.20.30.40",
	})
	d := &models.Droplet{ContainerNetwork: ptr("droplet-net")}
	if got := GetContainerIP(insp, d); got != "10.20.30.40" {
		t.Errorf("got %q, want 10.20.30.40", got)
	}
}

func TestGetContainerIPFallsBackToDefaultNetworkLiteral(t *testing.T) {
	// The Python code at admin.py:31 has a literal "default_network"
	// (not the same as dockerx.DefaultNetwork). Confirm we match.
	insp := makeInsp(map[string]string{
		"default_network": "10.0.0.7",
	})
	if got := GetContainerIP(insp, nil); got != "10.0.0.7" {
		t.Errorf("got %q, want 10.0.0.7", got)
	}
}

func TestGetContainerIPFallsBackToBridge(t *testing.T) {
	insp := makeInsp(map[string]string{
		"bridge": "172.17.0.2",
	})
	if got := GetContainerIP(insp, nil); got != "172.17.0.2" {
		t.Errorf("got %q, want 172.17.0.2", got)
	}
}

func TestGetContainerIPReturnsNAWhenNothingMatches(t *testing.T) {
	insp := makeInsp(map[string]string{
		"random-net": "10.99.99.1",
	})
	if got := GetContainerIP(insp, nil); got != FallbackIP {
		t.Errorf("got %q, want %q", got, FallbackIP)
	}
}

func TestGetContainerIPHandlesNilNetworkSettings(t *testing.T) {
	if got := GetContainerIP(types.ContainerJSON{}, nil); got != FallbackIP {
		t.Errorf("got %q, want %q (nil NetworkSettings)", got, FallbackIP)
	}

	if got := GetContainerIP(types.ContainerJSON{NetworkSettings: &types.NetworkSettings{}}, nil); got != FallbackIP {
		t.Errorf("got %q, want %q (nil Networks map)", got, FallbackIP)
	}
}

func TestGetContainerIPDropletNetworkBlankSkipped(t *testing.T) {
	insp := makeInsp(map[string]string{"bridge": "172.17.0.2"})
	d := &models.Droplet{ContainerNetwork: ptr("   ")} // whitespace
	if got := GetContainerIP(insp, d); got != "172.17.0.2" {
		t.Errorf("got %q, want bridge fallback when droplet net is blank", got)
	}
}
