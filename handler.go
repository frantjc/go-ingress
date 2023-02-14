package ingress

import (
	"net/http"
	"sort"
	"strings"
)

type handler struct {
	sorted   []string
	backends map[string]http.Handler
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	for _, pathPrefix := range h.sorted {
		if strings.HasPrefix(r.URL.Path, pathPrefix) {
			h.backends[pathPrefix].ServeHTTP(w, r)
			return
		}
	}

	http.NotFound(w, r)
}

func NewHandler(ps ...Path) http.Handler {
	var (
		sorted   = make([]string, len(ps))
		backends = make(map[string]http.Handler)
		i        = 0
	)
	for _, p := range ps {
		if _, ok := backends[p.Path()]; ok {
			panic("ingress: duplicate path")
		}
		backends[p.Path()] = p.Backend()
		sorted[i] = p.Path()
		i++
	}

	sort.Slice(sorted, func(i, j int) bool {
		return len(sorted[i]) > len(sorted[j])
	})

	return &handler{sorted, backends}
}
