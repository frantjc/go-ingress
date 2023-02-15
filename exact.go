package ingress

import (
	"net/http"
	"net/url"

	"github.com/frantjc/go-fn"
)

func ExactPath(path string, backend http.Handler) Path {
	cleaned, err := url.JoinPath("/", path)
	if err != nil {
		panic("ingress: invalid path")
	}

	return &exactPath{cleaned, backend}
}

type exactPath struct {
	path    string
	backend http.Handler
}

func (p *exactPath) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p.backend != nil {
		p.backend.ServeHTTP(w, r)
		return
	}

	http.NotFound(w, r)
}

func (p *exactPath) Matches(requestPath string) int {
	return fn.Ternary(p.path == requestPath, -1, 0)
}
