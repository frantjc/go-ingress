package ingress

import (
	"net/http"
	"net/url"
	"strings"
)

func PrefixPath(path string, backend http.Handler) Path {
	cleaned, err := url.JoinPath("/", path)
	if err != nil {
		panic("ingress: invalid path")
	}

	return &prefixPath{getElements(cleaned), backend}
}

type prefixPath struct {
	elements []string
	backend  http.Handler
}

func (p *prefixPath) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p.backend != nil {
		p.backend.ServeHTTP(w, r)
		return
	}

	http.NotFound(w, r)
}

func (p *prefixPath) Matches(requestPath string) int {
	elements := getElements(requestPath)

	if len(elements) < len(p.elements) {
		return 0
	}

	for i, element := range p.elements {
		if element != elements[i] {
			return 0
		}
	}

	return len(p.elements) + 1
}

func getElements(requestPath string) []string {
	elements := []string{}

	for _, element := range strings.Split(requestPath, "/") {
		if element != "" {
			elements = append(elements, element)
		}
	}

	return elements
}
