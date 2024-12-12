package ingress

import (
	"net/http"
	"net/url"
	"strings"
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

// reasonableMaxPathMatches is a guess at the most
// path matches that could be had in a URI. I saw 8000
// flying around as common convention for maximum URI length.
// Assuming the entire 8000 characters made up the request path
// and each element of the request path was one character
// (e.g. /a/b/c/.../x/y/z), we arrive at the reasonable 4000.
// Returning this as a number of matches should beat out any
// prefix match.
const reasonableMaxPathMatches = 4000

func (p *exactPath) Matches(requestPath string) int {
	if strings.Trim(p.path, "/") == strings.Trim(requestPath, "/") {
		return reasonableMaxPathMatches
	}

	return 0
}
