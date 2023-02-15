package ingress

import "net/http"

type Path interface {
	http.Handler
	// Matches takes a request's path and returns a "weight" representing
	// how strong of a match this path is to the request. <0 is infinity.
	Matches(string) int
}
