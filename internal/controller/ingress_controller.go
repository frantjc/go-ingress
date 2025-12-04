package controller

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strconv"
	"sync"

	"github.com/frantjc/go-ingress"
	"github.com/frantjc/go-ingress/api/v1alpha1"
	"github.com/frantjc/go-ingress/internal/logutil"
	xio "github.com/frantjc/x/io"
	xslices "github.com/frantjc/x/slices"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/transport/spdy"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// IngressReconciler reconciles a Ingress object
type IngressReconciler struct {
	// From manager via SetupWithManager.
	client.Client
	record.EventRecorder
	*rest.Config
	*kubernetes.Clientset
	// CLI args.
	GetIngressLoadBalancerIngress func(ctx context.Context) (*networkingv1.IngressLoadBalancerIngress, error)
	IngressClassName              string
	// Portforward to a Service-selected Pod instead of using Service DNS.
	// Useful for when running outside of the cluster we're reconciling.
	Portforward bool
	// Internal, only used when Portforward is true.
	svcKeyToForwardAddr sync.Map
	close               func() error
}

// +kubebuilder:rbac:groups=networking/v1,resources=ingresses;ingressclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking/v1,resources=ingresses/finalizers,verbs=update
// +kubebuilder:rbac:groups=networking/v1,resources=ingresses/status,verbs=update
// +kubebuilder:rbac:groups="",resources=pods;secrets;services,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods/portforwards,verbs=create
// +kubebuilder:rbac:groups=backend.ingress.frantj.cc,resources=proxies;redirects,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *IngressReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var (
		log = slog.New(logr.ToSlogHandler(ctrl.Log)).With("action", "Reconcile")
		ing = &networkingv1.Ingress{}
	)
	log.Info(req.String())

	if err := r.Get(ctx, req.NamespacedName, ing); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, err
	}

	ingressLoadBalancerIngress, err := r.GetIngressLoadBalancerIngress(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	if ing.Spec.IngressClassName != nil {
		if *ing.Spec.IngressClassName != r.IngressClassName {
			return ctrl.Result{}, nil
		}
	} else {
		if ing.Annotations != nil {
			if ingClassName, ok := ing.Annotations["kubernetes.io/ingress.class"]; ok {
				if ingClassName != r.IngressClassName {
					return ctrl.Result{}, nil
				}

				ing.Spec.IngressClassName = &ingClassName
				if err := r.Update(ctx, ing); err != nil {
					return ctrl.Result{}, err
				}

				return ctrl.Result{}, nil
			}
		}

		ingClass := &networkingv1.IngressClass{}

		if err := r.Get(ctx, client.ObjectKey{Name: r.IngressClassName}, ingClass); err != nil {
			return ctrl.Result{}, err
		}

		isDefaultIngressClass := false
		if ingClass.Annotations != nil {
			if rawIsDefaultClass, ok := ingClass.Annotations[networkingv1.AnnotationIsDefaultIngressClass]; ok {
				isDefaultIngressClass, _ = strconv.ParseBool(rawIsDefaultClass)
			}
		}

		if !isDefaultIngressClass {
			return ctrl.Result{}, nil
		}

		ing.Spec.IngressClassName = &ingClass.Name
		if err := r.Update(ctx, ing); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	ing.Status.LoadBalancer.Ingress = []networkingv1.IngressLoadBalancerIngress{*ingressLoadBalancerIngress}

	if err := r.Status().Update(ctx, ing); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *IngressReconciler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var (
		ctx     = req.Context()
		log     = logutil.SloggerFrom(ctx).With("action", "ServeHTTP", "host", req.Host, "path", req.URL.Path)
		ingList = &networkingv1.IngressList{}
	)
	log.Debug("serving")

	if err := r.List(ctx, ingList); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	paths := []ingress.Path{}

	for _, ing := range ingList.Items {
		_log := log.With("ingress", fmt.Sprintf("%s/%s", ing.Namespace, ing.Name))

		if ing.Spec.IngressClassName == nil {
			_log.Debug("skipping, ingress class name is not set")
			continue
		} else if *ing.Spec.IngressClassName != r.IngressClassName {
			_log.Debug("skipping, ingress class name does not match")
			continue
		}

		for _, ingRule := range ing.Spec.Rules {
			if ingRule.Host != req.Host {
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

				handler := http.HandlerFunc(func(_w http.ResponseWriter, _req *http.Request) {
					backend, err := r.handlerForPath(logutil.SloggerInto(ctx, _log), ing.Namespace, ingPath)
					if err != nil {
						_log.Error(err.Error())
						http.Error(_w, err.Error(), http.StatusInternalServerError)
						return
					}

					backend.ServeHTTP(_w, _req)
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
			handler := http.HandlerFunc(func(_w http.ResponseWriter, _req *http.Request) {
				backend, err := r.handlerForPath(logutil.SloggerInto(ctx, _log), ing.Namespace, networkingv1.HTTPIngressPath{
					Backend:  *ing.Spec.DefaultBackend,
					Path:     "/",
					PathType: &pathType,
				})
				if err != nil {
					_log.Error(err.Error())
					http.Error(_w, err.Error(), http.StatusInternalServerError)
					return
				}

				backend.ServeHTTP(_w, _req)
			})

			paths = append(paths, ingress.PrefixPath("/", handler))
		}
	}

	ingress.New(paths...).ServeHTTP(w, req)
}

func (r *IngressReconciler) GetCertificate(chi *tls.ClientHelloInfo) (*tls.Certificate, error) {
	var (
		// TODO(frantjc): Find a way to set chi.ctx so that we can use our context-propagated logger.
		ctx     = chi.Context()
		log     = slog.New(logr.ToSlogHandler(ctrl.Log)).With("serverName", chi.ServerName, "action", "GetCertificate")
		ingList = &networkingv1.IngressList{}
	)

	if err := r.List(ctx, ingList); err != nil {
		return nil, err
	}

	for _, ing := range ingList.Items {
		_log := log.With("ingress", fmt.Sprintf("%s/%s", ing.Namespace, ing.Name))

		if xslices.Some(ing.Spec.Rules, func(ingressRule networkingv1.IngressRule, _ int) bool {
			return ingressRule.Host == chi.ServerName
		}) {
			_log.Debug("found matching rule")

			ingTLS := xslices.Find(ing.Spec.TLS, func(ingTLS networkingv1.IngressTLS, _ int) bool {
				return slices.Contains(ingTLS.Hosts, chi.ServerName)
			})

			_log = _log.With("tlsSecret", fmt.Sprintf("%s/%s", ing.Namespace, ingTLS.SecretName))

			if ingTLS.SecretName != "" {
				_log.Debug("found matching tls")

				sec := &corev1.Secret{}

				if err := r.Get(ctx, client.ObjectKey{Namespace: ing.Namespace, Name: ingTLS.SecretName}, sec); err != nil {
					_log.Error(err.Error())
					return nil, err
				}

				crt, err := tls.X509KeyPair(sec.Data["tls.crt"], sec.Data["tls.key"])
				if err != nil {
					_log.Error(err.Error())
					return nil, err
				}

				return &crt, nil
			}
		}
	}

	return nil, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *IngressReconciler) SetupWithManager(mgr ctrl.Manager) (err error) {
	r.Client = mgr.GetClient()
	r.EventRecorder = mgr.GetEventRecorderFor("Ingresses")
	r.Config = mgr.GetConfig()
	if r.Clientset, err = kubernetes.NewForConfig(r.Config); err != nil {
		return err
	}
	// TODO(frantjc): Should we set up informers for other resources?
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.Ingress{}).
		Complete(r)
}

func (r *IngressReconciler) handlerForPath(ctx context.Context, namespace string, ingPath networkingv1.HTTPIngressPath) (http.Handler, error) {
	if ingPath.Backend.Service != nil {
		return r.handlerForService(ctx, namespace, *ingPath.Backend.Service)
	}

	if ingPath.PathType == nil || *ingPath.PathType != networkingv1.PathTypeImplementationSpecific || ingPath.Backend.Resource == nil || ingPath.Backend.Resource.APIGroup == nil {
		return nil, fmt.Errorf("unsupported ingress rule backend")
	}

	// TODO(frantjc): Support ConfigMap and Bucket backends.
	// TODO(frantjc): Middlewares for e.g. BasicAuth?
	group := *ingPath.Backend.Resource.APIGroup
	switch group {
	case "backend.ingress.frantj.cc":
		log := logutil.SloggerFrom(ctx).With("backend", fmt.Sprintf("%s.%s", ingPath.Backend.Resource.Kind, group))

		switch ingPath.Backend.Resource.Kind {
		case "Redirect":
			handler := http.HandlerFunc(func(_w http.ResponseWriter, _req *http.Request) {
				red := &v1alpha1.Redirect{}

				if err := r.Get(_req.Context(), client.ObjectKey{Namespace: namespace, Name: ingPath.Backend.Resource.Name}, red); err != nil {
					log.Error(err.Error())
					http.Error(_w, err.Error(), http.StatusInternalServerError)
					return
				}

				u, err := url.Parse(red.Spec.URL)
				if err != nil {
					log.Error(err.Error())
					http.Error(_w, err.Error(), http.StatusInternalServerError)
					return
				}

				http.Redirect(
					_w, _req,
					u.JoinPath(_req.URL.Path).String(),
					http.StatusMovedPermanently,
				)
			})

			return http.StripPrefix(ingPath.Path, handler), nil
		case "Proxy":
			handler := http.HandlerFunc(func(_w http.ResponseWriter, _req *http.Request) {
				pxy := &v1alpha1.Proxy{}

				if err := r.Get(_req.Context(), client.ObjectKey{Namespace: namespace, Name: ingPath.Backend.Resource.Name}, pxy); err != nil {
					log.Error(err.Error())
					http.Error(_w, err.Error(), http.StatusInternalServerError)
					return
				}

				u, err := url.Parse(pxy.Spec.URL)
				if err != nil {
					log.Error(err.Error())
					http.Error(_w, err.Error(), http.StatusInternalServerError)
					return
				}

				httputil.NewSingleHostReverseProxy(u.JoinPath(_req.URL.Path)).ServeHTTP(_w, _req)
			})

			return http.StripPrefix(ingPath.Path, handler), nil
		}
	}

	return nil, fmt.Errorf("unsupported ingress rule backend")
}

func (r *IngressReconciler) handlerForService(ctx context.Context, namespace string, ingressBackendService networkingv1.IngressServiceBackend) (http.Handler, error) {
	if r.Portforward {
		return r.handlerForPortforward(ctx, namespace, ingressBackendService)
	}

	var (
		targetPort = fmt.Sprint(ingressBackendService.Port.Number)
		svcKey     = client.ObjectKey{Namespace: namespace, Name: ingressBackendService.Name}
		log        = logutil.SloggerFrom(ctx).With("svc", svcKey.String())
	)
	if ingressBackendService.Port.Name != "" {
		log.Debug("finding service port number by name")
		svc := &corev1.Service{}

		if err := r.Get(ctx, svcKey, svc); err != nil {
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

func (r *IngressReconciler) handlerForPortforward(ctx context.Context, namespace string, ingressBackendService networkingv1.IngressServiceBackend) (http.Handler, error) {
	var (
		svcKey  = client.ObjectKey{Namespace: namespace, Name: ingressBackendService.Name}
		log     = logutil.SloggerFrom(ctx).With("svc", svcKey.String())
		podList = &corev1.PodList{}
		svc     = &corev1.Service{}
	)

	if forwardAddr, ok := r.svcKeyToForwardAddr.Load(svcKey.String()); ok {
		log.Debug("using existing portforward " + forwardAddr.(string))
		reverseProxy := httputil.NewSingleHostReverseProxy(&url.URL{
			Scheme: "http",
			Host:   forwardAddr.(string),
		})
		errorLog := slog.NewLogLogger(log.Handler(), slog.LevelError)
		reverseProxy.ErrorLog = errorLog
		return reverseProxy, nil
	}

	if err := r.Get(ctx, svcKey, svc); err != nil {
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

	if err := r.Client.List(ctx, podList, &client.ListOptions{
		Namespace:     namespace,
		LabelSelector: labels.SelectorFromSet(svc.Spec.Selector),
	}); err != nil {
		return nil, err
	}

	roundTripper, upgrader, err := spdy.RoundTripperFor(r.Config)
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
				r.CoreV1().
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
		if r.close == nil {
			r.close = func() error {
				return nil
			}
		}
		orig := r.close
		r.close = func() error {
			close(stopC)
			return orig()
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
		orig = r.close
		r.close = func() error {
			portforwarder.Close()
			return orig()
		}

		go func() {
			if err := portforwarder.ForwardPorts(); err != nil {
				log.Error(err.Error())
			}
			r.svcKeyToForwardAddr.Delete(svcKey.String())
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
				r.svcKeyToForwardAddr.Store(svcKey.String(), forwardAddr)
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

func (r *IngressReconciler) Close() error {
	if r.close != nil {
		return r.close()
	}
	return nil
}
