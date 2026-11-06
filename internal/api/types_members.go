package api

// MemberQuota is a sub-account's resource quota (0 = no limit): the sum of the CPU limits
// (cores) and memory limits (MB) of the services in its projects, and how many services.
type MemberQuota struct {
	CPU      float64 `json:"cpu,omitempty"`
	MemMB    int     `json:"mem_mb,omitempty"`
	Services int     `json:"services,omitempty"`
}

// MemberUsage is what counts against a MemberQuota now. Unlimited = services without a CPU or
// memory limit (they add nothing to that sum).
type MemberUsage struct {
	CPU       float64 `json:"cpu"`
	MemMB     int     `json:"mem_mb"`
	Services  int     `json:"services"`
	Unlimited int     `json:"unlimited,omitempty"`
}
