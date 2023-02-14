package ingress

import (
	"net/http"
	"net/url"
)

func NewPrefixPath(path string, backend http.Handler) Path {
	cleaned, err := url.JoinPath("/", path, "/")
	if err != nil {
		panic("ingress: invalid path")
	}
	return &prefixPath{cleaned, backend}
}

type prefixPath struct {
	path    string
	backend http.Handler
}

func (p *prefixPath) Backend() http.Handler {
	return p.backend
}

func (p *prefixPath) Path() string {
	return p.path
}
