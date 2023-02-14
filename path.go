package ingress

import "net/http"

const (
	PathTypePrefix = "Prefix"
)

type Path interface {
	Backend() http.Handler
	Path() string
	// Type() string only supporting Prefix for now
}
