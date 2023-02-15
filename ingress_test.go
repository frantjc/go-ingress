package ingress_test

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"

	"github.com/frantjc/go-ingress"
	"github.com/google/uuid"
)

func TestIngress(t *testing.T) {
	var (
		prefixBody  = uuid.NewString()
		exactBody   = uuid.NewString()
		defaultBody = "404 page not found\n" // from http.NotFound
	)

	// listen on a random port
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Error(err)
		t.FailNow()
	}
	t.Cleanup(func() {
		if err := l.Close(); err != nil {
			t.Error(err)
		}
	})

	// get the address of said port
	addr, err := url.Parse("http://" + l.Addr().String())
	if err != nil {
		panic(err)
	}

	// serve on the random port
	//nolint:errcheck,gosec
	go http.Serve(
		l,
		ingress.New(
			ingress.PrefixPath(
				"/prefix",
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Write([]byte(prefixBody))
				}),
			),
			ingress.ExactPath(
				"/exact",
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Write([]byte(exactBody))
				}),
			),
		),
	)

	for _, m := range []struct {
		path, expected string
	}{
		{"/notfound", defaultBody},
		{"/prefi", defaultBody},
		{"/exact/", defaultBody},
		{"/exact", exactBody},
		{"/Exact", defaultBody},
		{"/prefix", prefixBody},
		{"/prefix/", prefixBody},
		{"/Prefix/", defaultBody},
	} {
		res, err := http.Get(addr.JoinPath(m.path).String())
		if err != nil {
			panic(err)
		}

		b, err := io.ReadAll(res.Body)
		if err != nil {
			panic(err)
		}
		defer res.Body.Close()

		if string(b) != m.expected {
			t.Error("actual", string(b), "does not equal expected", m.expected)
			t.FailNow()
		}
	}
}
