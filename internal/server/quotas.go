package server

import (
	"context"
	"strconv"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// Member quotas (sub-accounts B3): an admin caps a member's CPU and memory (the sum of its
// services' limits) and its number of services; 0 = no limit. Checked when a member creates
// a service (POST /projects/{id}/services, one-click apps), changes a service's limits, and
// migrates a project (for every member that gains it on the target server). An operation is
// refused only when it adds to a dimension that would end over the quota, so a member an admin
// put over its quota can still shrink, stop or move what it has. Admins are never limited.

// Defaults a quota'd member's new service gets when it names no limit (the panel default is
// "no limit" for CPU): a quota is only meaningful when every service counts.
const (
	memberDefaultCPU   = 1.0
	memberDefaultMemMB = 512
)

func fmtCores(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func validateQuota(q api.MemberQuota) error {
	if q.CPU < 0 || q.CPU > maxCPULimit || q.CPU != q.CPU {
		return badRequest("quota cpu must be 0 (no limit) or up to %d cores", maxCPULimit)
	}
	if q.MemMB < 0 || (q.MemMB > 0 && q.MemMB < minMemLimitMB) || q.MemMB > maxMemLimitMB {
		return badRequest("quota mem_mb must be 0 (no limit) or between %d and %d", minMemLimitMB, maxMemLimitMB)
	}
	if q.Services < 0 || q.Services > 100000 {
		return badRequest("quota services must be 0 (no limit) or a positive number")
	}
	return nil
}

// quotaFill gives a quota'd member's new service the limits it must have. explicitCPU /
// explicitMem: the request asked for "no limit" (-1), which a quota does not allow.
func (s *Server) quotaFill(ctx context.Context, v *api.Service, explicitCPU, explicitMem bool) error {
	p := principalFrom(ctx)
	if p == nil || !p.Member {
		return nil
	}
	q, err := s.st.MemberQuota(ctx, p.User.ID)
	if err != nil {
		return err
	}
	if q.CPU > 0 && v.CPULimit == 0 {
		if explicitCPU {
			return errf(403, "quota_limit_required", "your account has a CPU quota (%s cores): a service needs a CPU limit", fmtCores(q.CPU))
		}
		v.CPULimit = memberDefaultCPU
	}
	if q.MemMB > 0 && v.MemLimitMB == 0 {
		if explicitMem {
			return errf(403, "quota_limit_required", "your account has a memory quota (%d MB): a service needs a memory limit", q.MemMB)
		}
		v.MemLimitMB = memberDefaultMemMB
	}
	return nil
}

// quotaLimits: a quota'd member cannot take a service's limit away (set it to 0 = no limit).
func (s *Server) quotaLimits(ctx context.Context, v api.Service) error {
	p := principalFrom(ctx)
	if p == nil || !p.Member {
		return nil
	}
	q, err := s.st.MemberQuota(ctx, p.User.ID)
	if err != nil {
		return err
	}
	if q.CPU > 0 && v.CPULimit <= 0 {
		return errf(403, "quota_limit_required", "your account has a CPU quota (%s cores): a service needs a CPU limit", fmtCores(q.CPU))
	}
	if q.MemMB > 0 && v.MemLimitMB <= 0 {
		return errf(403, "quota_limit_required", "your account has a memory quota (%d MB): a service needs a memory limit", q.MemMB)
	}
	return nil
}

// checkQuota: the calling member's usage plus add minus remove must stay within its quota
// in every dimension the change increases.
func (s *Server) checkQuota(ctx context.Context, add, remove api.MemberUsage) error {
	p := principalFrom(ctx)
	if p == nil || !p.Member {
		return nil
	}
	return s.checkQuotaOf(ctx, p.User.ID, add, remove, "your account")
}

func (s *Server) checkQuotaOf(ctx context.Context, userID int64, add, remove api.MemberUsage, who string) error {
	q, err := s.st.MemberQuota(ctx, userID)
	if err != nil || q == (api.MemberQuota{}) {
		return err
	}
	u, err := s.st.MemberUsage(ctx, userID)
	if err != nil {
		return err
	}
	if q.Services > 0 && add.Services > remove.Services && u.Services-remove.Services+add.Services > q.Services {
		return errf(403, "quota_exceeded", "service quota exceeded: %s may run %d services and has %d", who, q.Services, u.Services)
	}
	if q.CPU > 0 && add.CPU > remove.CPU && u.CPU-remove.CPU+add.CPU > q.CPU+1e-9 {
		return errf(403, "quota_exceeded", "CPU quota exceeded: %s has %s of %s cores in use and this needs %s more",
			who, fmtCores(u.CPU), fmtCores(q.CPU), fmtCores(add.CPU-remove.CPU))
	}
	if q.MemMB > 0 && add.MemMB > remove.MemMB && u.MemMB-remove.MemMB+add.MemMB > q.MemMB {
		return errf(403, "quota_exceeded", "memory quota exceeded: %s has %d of %d MB in use and this needs %d MB more",
			who, u.MemMB, q.MemMB, add.MemMB-remove.MemMB)
	}
	return nil
}

// checkMigrateQuota: moving project projectID from server from to server to adds it to every
// member that has the target and not the source (when a member asks for the move).
func (s *Server) checkMigrateQuota(ctx context.Context, projectID, from, to string) error {
	p := principalFrom(ctx)
	if p == nil || !p.Member {
		return nil
	}
	gain, err := s.st.ProjectUsage(ctx, projectID)
	if err != nil {
		return err
	}
	onTo, err := s.st.MembersOnServer(ctx, to)
	if err != nil {
		return err
	}
	had := map[int64]bool{}
	if onFrom, err := s.st.MembersOnServer(ctx, from); err == nil {
		for _, m := range onFrom {
			had[m.ID] = true
		}
	}
	for _, m := range onTo {
		if had[m.ID] {
			continue
		}
		who := "another sub-account on the target server"
		if m.ID == p.User.ID {
			who = "your account"
		}
		if err := s.checkQuotaOf(ctx, m.ID, gain, api.MemberUsage{}, who); err != nil {
			return err
		}
	}
	return nil
}
