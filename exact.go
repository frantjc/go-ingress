package ingress

import (
	"math"
	"net/http"
	"net/url"
	"strings"
)

type ExactPathOpt func(*exactPath)

func WithMatchIgnoreSlash(p *exactPath) {
	p.ignoreTrailingSlash = true
}

func ExactPath(path string, backend http.Handler, opts ...ExactPathOpt) Path {
	cleaned, err := url.JoinPath("/", path)
	if err != nil {
		panic("ingress: invalid path")
	}

	p := &exactPath{cleaned, backend, false}

	for _, opt := range opts {
		opt(p)
	}

	return p
}

type exactPath struct {
	path                string
	backend             http.Handler
	ignoreTrailingSlash bool
}

func (p *exactPath) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p.backend != nil {
		p.backend.ServeHTTP(w, r)
		return
	}

	http.NotFound(w, r)
}

func (p *exactPath) Matches(requestPath string) int {
	if p.ignoreTrailingSlash {
		if strings.TrimSuffix(p.path, "/") == strings.TrimSuffix(requestPath, "/") {
			return math.MaxInt
		}
	}

	if p.path == requestPath {
		return math.MaxInt
	}

	return 0
}
