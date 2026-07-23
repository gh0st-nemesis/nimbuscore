package netpolicy

import (
	"strings"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

func Allowed(policies []*v1.NetworkPolicy, sourceLabels, destLabels map[string]string, protocol string, port int32) bool {
	for _, p := range policies {
		if !selectorMatches(p.GetSpec().GetPodSelector(), destLabels) {
			continue
		}
		for _, ingress := range p.GetSpec().GetIngress() {
			if portAllowed(ingress.GetPorts(), protocol, port) && peerAllowed(ingress.GetFrom(), sourceLabels) {
				return true
			}
		}
	}
	return false
}

func selectorMatches(selector, labels map[string]string) bool {
	for k, v := range selector {
		if labels[k] != v {
			return false
		}
	}
	return true
}

func portAllowed(ports []*v1.NetworkPolicyPort, protocol string, port int32) bool {
	if len(ports) == 0 {
		return true
	}
	for _, p := range ports {
		if p.GetPort() != 0 && p.GetPort() != port {
			continue
		}
		if p.GetProtocol() != "" && !strings.EqualFold(p.GetProtocol(), protocol) {
			continue
		}
		return true
	}
	return false
}

func peerAllowed(peers []*v1.NetworkPolicyPeer, sourceLabels map[string]string) bool {
	if len(peers) == 0 {
		return true
	}
	for _, peer := range peers {
		if selectorMatches(peer.GetPodSelector(), sourceLabels) {
			return true
		}
	}
	return false
}
