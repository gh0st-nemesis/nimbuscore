package rbac

import (
	"slices"
	"strings"
)

const Wildcard = "*"

type Rule struct {
	Resources []string
	Verbs     []string
}

func (r Rule) allows(resource, verb string) bool {
	return (slices.Contains(r.Resources, Wildcard) || slices.Contains(r.Resources, resource)) &&
		(slices.Contains(r.Verbs, Wildcard) || slices.Contains(r.Verbs, verb))
}

type Role struct {
	Name  string
	Rules []Rule
}

func (r Role) allows(resource, verb string) bool {
	for _, rule := range r.Rules {
		if rule.allows(resource, verb) {
			return true
		}
	}
	return false
}

type Binding struct {
	PathPrefix string
	Role       Role
}

type Authorizer struct {
	bindings []Binding
}

func NewAuthorizer(bindings ...Binding) *Authorizer {
	return &Authorizer{bindings: bindings}
}

func (a *Authorizer) Allow(idPath, resource, verb string) bool {
	for _, b := range a.bindings {
		if strings.HasPrefix(idPath, b.PathPrefix) && b.Role.allows(resource, verb) {
			return true
		}
	}
	return false
}
