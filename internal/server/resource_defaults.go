package server

import (
	"context"
	"math"
	"strconv"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// Default resource limits for new services (settings "default_mem_limit_mb" and
// "default_cpu_limit"; 0 = no limit). A service created without a limit gets these; existing
// services are never changed by them.
const (
	defaultMemLimitMB = 1024 // when the setting was never saved
	defaultCPULimit   = 0.0
	// Docker refuses memory limits below 6 MB.
	minMemLimitMB = 6
	maxMemLimitMB = 1 << 20 // 1 TiB
	maxCPULimit   = 1024
)

// resourceDefaults returns the limits a new service without its own gets.
func (s *Server) resourceDefaults(ctx context.Context) (memMB int, cpu float64) {
	memMB, cpu = defaultMemLimitMB, defaultCPULimit
	if v, _ := s.st.GetSetting(ctx, "default_mem_limit_mb"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			memMB = n
		}
	}
	if v, _ := s.st.GetSetting(ctx, "default_cpu_limit"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 && !math.IsInf(f, 0) {
			cpu = f
		}
	}
	return memMB, cpu
}

func (s *Server) fillResourceDefaults(ctx context.Context, st *api.Settings) {
	mem, cpu := s.resourceDefaults(ctx)
	st.DefaultMemLimitMB, st.DefaultCPULimit = &mem, &cpu
}

// applyResourceDefaultsSettings validates the default limits of a PUT /settings into set;
// a field left out (null) keeps the stored value.
func applyResourceDefaultsSettings(req api.Settings, set map[string]string) error {
	if m := req.DefaultMemLimitMB; m != nil {
		if *m < 0 || (*m > 0 && *m < minMemLimitMB) || *m > maxMemLimitMB {
			return badRequest("default_mem_limit_mb must be 0 (no limit) or between %d and %d", minMemLimitMB, maxMemLimitMB)
		}
		set["default_mem_limit_mb"] = strconv.Itoa(*m)
	}
	if c := req.DefaultCPULimit; c != nil {
		if math.IsNaN(*c) || *c < 0 || *c > maxCPULimit {
			return badRequest("default_cpu_limit must be 0 (no limit) or up to %d cores", maxCPULimit)
		}
		set["default_cpu_limit"] = strconv.FormatFloat(*c, 'f', -1, 64)
	}
	return nil
}

// withDefaultLimits fills the limits of a service being created: 0 (not given) takes the
// default, -1 asks for no limit explicitly (stored as 0).
func (s *Server) withDefaultLimits(ctx context.Context, v *api.Service) {
	mem, cpu := s.resourceDefaults(ctx)
	switch {
	case v.MemLimitMB == -1:
		v.MemLimitMB = 0
	case v.MemLimitMB == 0:
		v.MemLimitMB = mem
	}
	switch {
	case v.CPULimit == -1:
		v.CPULimit = 0
	case v.CPULimit == 0:
		v.CPULimit = cpu
	}
}
