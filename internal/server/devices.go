package server

// Host devices (api.Service.Devices, docker --device) and the /dev/shm size
// (api.Service.ShmSizeMB). Agents before v0.1.50 ignore both fields and would start the
// container without them, so a spec that uses them is never sent to such an agent.

import (
	"context"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// usesDevices reports whether a spec needs an agent with HelloPayload.Devices.
func usesDevices(devices []string, shmMB int) bool {
	return len(devices) > 0 || shmMB > 0
}

func (s *Server) agentDevices(nodeID string) bool {
	c := s.hub.get(nodeID)
	return c != nil && c.devices.Load()
}

// checkDeviceCaps refuses devices or a /dev/shm size for an online agent that would ignore
// them. An offline server is checked again when the deployment is sent (sendApply).
func (s *Server) checkDeviceCaps(nodeID string, devices []string, shmMB int) error {
	if !usesDevices(devices, shmMB) || !s.hub.Online(nodeID) || s.agentDevices(nodeID) {
		return nil
	}
	return errf(409, "agent_outdated", "devices and shm_size_mb need atoll-agent v0.1.50 or newer on this server; upgrade the agent first")
}

// checkServiceDeviceCaps is checkDeviceCaps for an edit of svc into next.
func (s *Server) checkServiceDeviceCaps(ctx context.Context, svc *api.Service, next api.Service) error {
	nodeID, err := s.serviceNode(ctx, svc)
	if err != nil {
		return nil // the deploy reports it
	}
	return s.checkDeviceCaps(nodeID, next.Devices, next.ShmSizeMB)
}

// validateDevices normalises and checks the device and /dev/shm fields of v.
func validateDevices(v *api.Service) error {
	seen := map[string]bool{}
	out := []string{}
	for _, d := range v.Devices {
		if !api.AllowedDevice(d) {
			return badRequest("device %q cannot be mapped; allowed: %s", d, api.AllowedDevices)
		}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	v.Devices = out
	if len(out) == 0 {
		v.Devices = nil
	}
	if v.ShmSizeMB < 0 || v.ShmSizeMB > api.MaxShmSizeMB {
		return badRequest("shm_size_mb must be 0-%d", api.MaxShmSizeMB)
	}
	return nil
}
