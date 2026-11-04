// Package tenant separates users or teams that share one installation. Every
// tenant reads the same information base (articles, clusters, entities, base
// scores) but owns its watches, labels, alert state and read markers. A
// single-user install has one tenant, "default", and never needs to know.
//
// On disk a tenant is a folder under the configuration root:
//
//	tenants/<id>/tenant.yaml
//	tenants/<id>/watches/<watch-id>.yaml
package tenant

import "github.com/b070nd/stoxicity/app/src/model"

// Default is the tenant used when none is named.
const Default = "default"

// Tenant is one workspace.
type Tenant struct {
	ID           string `yaml:"id"`
	Name         string `yaml:"name"`
	DefaultWatch string `yaml:"default_watch"` // opened on start
	Dir          string `yaml:"-"`             // its configuration folder
}

// Valid reports whether the ID may be used.
func (t Tenant) Valid() bool { return model.ValidID(t.ID) }

// Registry finds tenants.
type Registry interface {
	List() ([]Tenant, error)
	Get(id string) (Tenant, error)
}

// Resolve picks the tenant to use: the command-line flag wins, then the
// environment, then fallback.
func Resolve(flag, env, fallback string) string {
	if flag != "" {
		return flag
	}
	if env != "" {
		return env
	}
	return fallback
}
