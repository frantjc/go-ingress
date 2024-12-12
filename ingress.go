package ingress

import "net/http"

type Ingress struct {
	Paths          []Path
	DefaultBackend http.Handler
}

func (i *Ingress) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var (
		contender = i.DefaultBackend
		strongest = 0
	)

	for _, p := range i.Paths {
		if weight := p.Matches(r.URL.Path); weight > strongest {
			strongest = weight
			contender = p
		}
	}

	if contender == nil {
		contender = http.NotFoundHandler()
	}

	contender.ServeHTTP(w, r)
}

func New(paths ...Path) *Ingress {
	return &Ingress{
		Paths:          paths,
		DefaultBackend: http.NotFoundHandler(),
	}
}
