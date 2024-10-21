package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"

	"github.com/frantjc/go-ingress"
)

func main() {
	// Listen on a random port.
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		panic(err)
	}
	defer l.Close()

	// Get the address of said port.
	addr, err := url.Parse("http://" + l.Addr().String())
	if err != nil {
		panic(err)
	}

	go http.Serve(
		l,
		ingress.New(
			ingress.PrefixPath(
				"/prefix",
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Write([]byte("Prefix\n"))
				}),
			),
			ingress.ExactPath(
				"/exact",
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Write([]byte("Exact\n"))
				}),
			),
		),
	)

	for _, path := range []string{
		"/notfound",
		"/exact/",
		"/exact",
		"/prefix",
		"/prefix/",
	} {
		res, err := http.Get(addr.JoinPath(path).String())
		if err != nil {
			panic(err)
		}
		defer res.Body.Close()

		b, err := io.ReadAll(res.Body)
		if err != nil {
			panic(err)
		}

		fmt.Println(path, " => ", string(b))
	}
	// /notfound  =>  404 page not found.

	// /exact/  =>  404 page not found.

	// /exact  =>  Exact.

	// /prefix  =>  Prefix.

	// /prefix/  =>  Prefix.
}
