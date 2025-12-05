package controller

import (
	"context"
	"encoding/base64"
	"fmt"
	"github.com/frantjc/go-ingress"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/frantjc/go-ingress/api/v1alpha1"
	"github.com/frantjc/go-ingress/internal/logutil"
	xio "github.com/frantjc/x/io"
	xslices "github.com/frantjc/x/slices"
	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// +kubebuilder:rbac:groups="",resources=pods;secrets;services,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods/portforwards,verbs=create
// +kubebuilder:rbac:groups=backend.ingress.frantj.cc,resources=basicauths;proxies;redirects,verbs=get;list;watch

func (c *IngressController) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var (
		ctx     = r.Context()
		log     = logutil.SloggerFrom(ctx).With("action", "ServeHTTP", "host", r.Host, "path", r.URL.Path)
		ingList = &networkingv1.IngressList{}
	)
	log.Debug("serving")

	if err := c.List(ctx, ingList); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	paths := []ingress.Path{}

	for _, ing := range ingList.Items {
		_log := log.With("ingress", fmt.Sprintf("%s/%s", ing.Namespace, ing.Name))

		if ing.Spec.IngressClassName == nil {
			_log.Debug("skipping, ingress class name is not set")
			continue
		} else if *ing.Spec.IngressClassName != c.IngressClassName {
			_log.Debug("skipping, ingress class name does not match")
			continue
		}

		for _, ingRule := range ing.Spec.Rules {
			if ingRule.Host != r.Host {
				continue
			}

			if ingRule.HTTP == nil {
				continue
			}

			_log.Debug("found matching rule")

			for _, ingPath := range ingRule.HTTP.Paths {
				if ingPath.PathType == nil {
					continue
				}

				handler := http.HandlerFunc(func(_w http.ResponseWriter, _r *http.Request) {
					backend, err := c.handlerForPath(logutil.SloggerInto(ctx, _log), ing.Namespace, ingPath)
					if err != nil {
						_log.Error(err.Error())
						http.Error(_w, err.Error(), http.StatusInternalServerError)
						return
					}

					backend.ServeHTTP(_w, _r)
				})

				switch *ingPath.PathType {
				case networkingv1.PathTypeExact:
					paths = append(paths, ingress.ExactPath(ingPath.Path, handler))
				case networkingv1.PathTypePrefix:
					paths = append(paths, ingress.PrefixPath(ingPath.Path, handler))
				case networkingv1.PathTypeImplementationSpecific:
					paths = append(paths, ingress.PrefixPath(ingPath.Path, handler))
				}
			}
		}

		if len(paths) == 0 && ing.Spec.DefaultBackend != nil {
			_log.Debug("no rule paths matched, using default backend")
			pathType := networkingv1.PathTypePrefix
			handler := http.HandlerFunc(func(_w http.ResponseWriter, _r *http.Request) {
				backend, err := c.handlerForPath(logutil.SloggerInto(ctx, _log), ing.Namespace, networkingv1.HTTPIngressPath{
					Backend:  *ing.Spec.DefaultBackend,
					Path:     "/",
					PathType: &pathType,
				})
				if err != nil {
					_log.Error(err.Error())
					http.Error(_w, err.Error(), http.StatusInternalServerError)
					return
				}

				backend.ServeHTTP(_w, _r)
			})

			paths = append(paths, ingress.PrefixPath("/", handler))
		}
	}

	ingress.New(paths...).ServeHTTP(w, r)
}

func (c *IngressController) handlerForPath(ctx context.Context, namespace string, ingPath networkingv1.HTTPIngressPath) (http.Handler, error) {
	if ingPath.Backend.Service != nil {
		return c.handlerForService(ctx, namespace, *ingPath.Backend.Service)
	}

	if ingPath.PathType == nil || *ingPath.PathType != networkingv1.PathTypeImplementationSpecific || ingPath.Backend.Resource == nil || ingPath.Backend.Resource.APIGroup == nil {
		return nil, fmt.Errorf("unsupported ingress backend")
	}

	// TODO(frantjc): Support ConfigMap and Bucket backends.
	group := *ingPath.Backend.Resource.APIGroup
	switch group {
	case v1alpha1.GroupVersion.Group:
		log := logutil.SloggerFrom(ctx).With("backend", fmt.Sprintf("%s.%s", strings.ToLower(ingPath.Backend.Resource.Kind), group))

		switch ingPath.Backend.Resource.Kind {
		case "Redirect":
			handler := http.HandlerFunc(func(_w http.ResponseWriter, _r *http.Request) {
				redirect := &v1alpha1.Redirect{}

				if err := c.Get(_r.Context(), client.ObjectKey{Namespace: namespace, Name: ingPath.Backend.Resource.Name}, redirect); err != nil {
					log.Error(err.Error())
					http.Error(_w, err.Error(), http.StatusInternalServerError)
					return
				}

				u, err := url.Parse(redirect.Spec.URL)
				if err != nil {
					log.Error(err.Error())
					http.Error(_w, err.Error(), http.StatusInternalServerError)
					return
				}

				http.Redirect(
					_w, _r,
					u.JoinPath(_r.URL.Path).String(),
					http.StatusMovedPermanently,
				)
			})

			return http.StripPrefix(ingPath.Path, handler), nil
		case "Proxy":
			handler := http.HandlerFunc(func(_w http.ResponseWriter, _r *http.Request) {
				proxy := &v1alpha1.Proxy{}

				if err := c.Get(_r.Context(), client.ObjectKey{Namespace: namespace, Name: ingPath.Backend.Resource.Name}, proxy); err != nil {
					log.Error(err.Error())
					http.Error(_w, err.Error(), http.StatusInternalServerError)
					return
				}

				u, err := url.Parse(proxy.Spec.URL)
				if err != nil {
					log.Error(err.Error())
					http.Error(_w, err.Error(), http.StatusInternalServerError)
					return
				}

				httputil.NewSingleHostReverseProxy(u.JoinPath(_r.URL.Path)).ServeHTTP(_w, _r)
			})

			return http.StripPrefix(ingPath.Path, handler), nil
		case "BasicAuth":
			handler := http.HandlerFunc(func(_w http.ResponseWriter, _r *http.Request) {
				basicAuth := &v1alpha1.BasicAuth{}

				if err := c.Get(_r.Context(), client.ObjectKey{Namespace: namespace, Name: ingPath.Backend.Resource.Name}, basicAuth); err != nil {
					log.Error(err.Error())
					http.Error(_w, err.Error(), http.StatusInternalServerError)
					return
				}

				secRef := basicAuth.Spec.SecretKeyRef
				if secRef.Name == "" || secRef.Key == "" {
					log.Error("invalid secretKeyRef in BasicAuth")
					http.Error(_w, "invalid basic auth secret reference", http.StatusInternalServerError)
					return
				}

				sec := &corev1.Secret{}
				if err := c.Get(_r.Context(), client.ObjectKey{Namespace: namespace, Name: secRef.Name}, sec); err != nil {
					log.Error(err.Error())
					http.Error(_w, err.Error(), http.StatusInternalServerError)
					return
				}

				raw, ok := sec.Data[secRef.Key]
				if !ok {
					log.Error("secret key not found")
					http.Error(_w, "basic auth secret key not found", http.StatusInternalServerError)
					return
				}

				creds := map[string]string{}
				for _, line := range strings.Split(string(raw), "\n") {
					line = strings.TrimSpace(line)
					if line == "" {
						continue
					}
					user, pass, ok := strings.Cut(line, ":")
					if !ok {
						continue
					}
					creds[user] = pass
				}

				auth := _r.Header.Get("Authorization")
				if auth == "" || !strings.HasPrefix(auth, "Basic ") {
					_w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Basic realm="%s"`, basicAuth.Name))
					http.Error(_w, "401 not authorized", http.StatusUnauthorized)
					return
				}

				b64 := strings.TrimPrefix(auth, "Basic ")
				decoded, err := base64.StdEncoding.DecodeString(b64)
				if err != nil {
					_w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Basic realm="%s"`, basicAuth.Name))
					http.Error(_w, "401 not authorized", http.StatusUnauthorized)
					return
				}

				user, pass, ok := strings.Cut(string(decoded), ":")
				if !ok {
					_w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Basic realm="%s"`, basicAuth.Name))
					http.Error(_w, "401 not authorized", http.StatusUnauthorized)
					return
				}

				hash, ok := creds[user]
				if !ok {
					_w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Basic realm="%s"`, basicAuth.Name))
					http.Error(_w, "401 not authorized", http.StatusUnauthorized)
					return
				}

				if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(pass)); err != nil {
					_w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Basic realm="%s"`, basicAuth.Name))
					http.Error(_w, "401 not authorized", http.StatusUnauthorized)
					return
				}

				forwardHandler, err := c.handlerForPath(logutil.SloggerInto(_r.Context(), log), namespace, basicAuth.Spec.HTTPIngressPath)
				if err != nil {
					log.Error(err.Error())
					http.Error(_w, err.Error(), http.StatusInternalServerError)
					return
				}

				forwardHandler.ServeHTTP(_w, _r)
			})

			return http.StripPrefix(ingPath.Path, handler), nil
		}
	}

	return nil, fmt.Errorf("unsupported ingress backend")
}

func (c *IngressController) handlerForService(ctx context.Context, namespace string, ingressBackendService networkingv1.IngressServiceBackend) (http.Handler, error) {
	if c.Portforward {
		return c.handlerForPortforward(ctx, namespace, ingressBackendService)
	}

	var (
		targetPort = fmt.Sprint(ingressBackendService.Port.Number)
		svcKey     = client.ObjectKey{Namespace: namespace, Name: ingressBackendService.Name}
		log        = logutil.SloggerFrom(ctx).With("svc", svcKey.String())
	)
	if ingressBackendService.Port.Name != "" {
		log.Debug("finding service port number by name")
		svc := &corev1.Service{}

		if err := c.Get(ctx, svcKey, svc); err != nil {
			return nil, err
		}

		svcPort := xslices.Find(svc.Spec.Ports, func(svcPort corev1.ServicePort, _ int) bool {
			return svcPort.Name == ingressBackendService.Port.Name
		})

		if svcPort.Port == 0 {
			return nil, fmt.Errorf("unknown service port name %s", ingressBackendService.Port.Name)
		} else if svcPort.Protocol != corev1.ProtocolTCP {
			return nil, fmt.Errorf("unsupported service port protocol %s", svcPort.Protocol)
		}

		targetPort = fmt.Sprint(svcPort.Port)
	}

	reverseProxy := httputil.NewSingleHostReverseProxy(&url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s.%s.svc.cluster.local:%s", svcKey.Name, namespace, targetPort),
	})
	errorLog := slog.NewLogLogger(log.Handler(), slog.LevelError)
	reverseProxy.ErrorLog = errorLog
	return reverseProxy, nil
}

func (c *IngressController) handlerForPortforward(ctx context.Context, namespace string, ingressBackendService networkingv1.IngressServiceBackend) (http.Handler, error) {
	var (
		svcKey  = client.ObjectKey{Namespace: namespace, Name: ingressBackendService.Name}
		log     = logutil.SloggerFrom(ctx).With("svc", svcKey.String())
		podList = &corev1.PodList{}
		svc     = &corev1.Service{}
	)

	if forwardAddr, ok := c.svcKeyToForwardAddr.Load(svcKey.String()); ok {
		log.Debug("using existing portforward " + forwardAddr.(string))
		reverseProxy := httputil.NewSingleHostReverseProxy(&url.URL{
			Scheme: "http",
			Host:   forwardAddr.(string),
		})
		errorLog := slog.NewLogLogger(log.Handler(), slog.LevelError)
		reverseProxy.ErrorLog = errorLog
		return reverseProxy, nil
	}

	if err := c.Get(ctx, svcKey, svc); err != nil {
		return nil, err
	}

	svcPort := xslices.Find(svc.Spec.Ports, func(svcPort corev1.ServicePort, _ int) bool {
		return svcPort.Name == ingressBackendService.Port.Name
	})

	if svcPort.Protocol != corev1.ProtocolTCP {
		return nil, fmt.Errorf("unsupported service port protocol %s", svcPort.Protocol)
	}

	targetPort := svcPort.TargetPort.String()
	log = log.With("targetPort", targetPort)

	if err := c.Client.List(ctx, podList, &client.ListOptions{
		Namespace:     namespace,
		LabelSelector: labels.SelectorFromSet(svc.Spec.Selector),
	}); err != nil {
		return nil, err
	}

	roundTripper, upgrader, err := spdy.RoundTripperFor(c.Config)
	if err != nil {
		return nil, err
	}

	for _, pod := range podList.Items {
		var (
			log    = log.With("pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name))
			dialer = spdy.NewDialer(
				upgrader,
				&http.Client{Transport: roundTripper},
				http.MethodPost,
				c.CoreV1().
					RESTClient().
					Post().
					Resource("pods").
					Namespace(pod.Namespace).
					Name(pod.Name).
					SubResource("portforward").
					URL(),
			)
			stopC  = make(chan struct{}, 1)
			readyC = make(chan struct{}, 1)
		)
		if c.close == nil {
			c.close = func() error {
				return nil
			}
		}
		origClose := c.close
		c.close = func() error {
			close(stopC)
			return origClose()
		}
		log.Debug("portforwarding")

		portforwarder, err := portforward.New(
			dialer,
			// Choose any available port--this is ephemeral, and we can get it back from portforwarder.GetPorts().
			[]string{fmt.Sprintf(":%s", targetPort)},
			stopC, readyC,
			xio.WriterFunc(func(b []byte) (int, error) {
				log.Debug(string(b))
				return len(b), nil
			}),
			xio.WriterFunc(func(b []byte) (int, error) {
				log.Error(string(b))
				return len(b), nil
			}),
		)
		if err != nil {
			return nil, err
		}
		origClose = c.close
		c.close = func() error {
			portforwarder.Close()
			return origClose()
		}

		go func() {
			if err := portforwarder.ForwardPorts(); err != nil {
				log.Error(err.Error())
			}
			c.svcKeyToForwardAddr.Delete(svcKey.String())
		}()
		<-readyC

		forwardedPorts, err := portforwarder.GetPorts()
		if err != nil {
			return nil, err
		}

		for _, forwardedPort := range forwardedPorts {
			if fmt.Sprint(forwardedPort.Remote) == targetPort {
				forwardAddr := fmt.Sprintf("127.0.0.1:%d", forwardedPort.Local)
				log.Debug("portforwarded " + forwardAddr)
				c.svcKeyToForwardAddr.Store(svcKey.String(), forwardAddr)
				reverseProxy := httputil.NewSingleHostReverseProxy(&url.URL{
					Scheme: "http",
					Host:   forwardAddr,
				})
				errorLog := slog.NewLogLogger(log.Handler(), slog.LevelError)
				reverseProxy.ErrorLog = errorLog
				return reverseProxy, nil
			}
		}
	}

	return nil, fmt.Errorf("unable to portforward to any Pods")
}

func (c *IngressController) Close() error {
	if c.close != nil {
		return c.close()
	}
	return nil
}
