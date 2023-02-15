package ingress

import "net/http"

type paths []Path

func (ps paths) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var (
		contender = http.NotFoundHandler()
		strongest = 0
	)

	for _, p := range ps {
		weight := p.Matches(r.URL.Path)
		if weight < 0 {
			p.ServeHTTP(w, r)
			return
		} else if weight > strongest {
			strongest = weight
			contender = p
		}
	}

	contender.ServeHTTP(w, r)
}

func New(ps ...Path) http.Handler {
	hndlr := make(paths, len(ps))
	copy(hndlr, ps)
	return hndlr
}
