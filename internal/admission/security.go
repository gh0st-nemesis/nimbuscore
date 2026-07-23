package admission

import (
	"context"
	"fmt"
	"slices"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

const DefaultSeccompProfile = "RuntimeDefault"

type SecurityContextPolicy struct {
	AllowedCapabilities []string
}

func NewSecurityContextPolicy(allowedCapabilities ...string) *SecurityContextPolicy {
	return &SecurityContextPolicy{AllowedCapabilities: allowedCapabilities}
}

func (p *SecurityContextPolicy) Admit(_ context.Context, req *Request) error {
	for _, c := range req.Spec.GetContainers() {
		sc := c.GetSecurityContext()
		if sc == nil {
			sc = &v1.SecurityContext{}
			c.SecurityContext = sc
		}

		if sc.GetSeccompProfile() == "" {
			sc.SeccompProfile = DefaultSeccompProfile
		}

		if sc.GetPrivileged() {
			return fmt.Errorf("container %q: privileged containers are not permitted", c.GetName())
		}
		if sc.GetAllowPrivilegeEscalation() {
			return fmt.Errorf("container %q: privilege escalation is not permitted", c.GetName())
		}
		for _, cap := range sc.GetAddCapabilities() {
			if !slices.Contains(p.AllowedCapabilities, cap) {
				return fmt.Errorf("container %q: capability %q is not in the cluster's allowlist", c.GetName(), cap)
			}
		}
	}
	return nil
}
