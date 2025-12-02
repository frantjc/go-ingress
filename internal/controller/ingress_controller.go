package controller

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"sync"

	"github.com/frantjc/go-ingress"
	"github.com/frantjc/go-ingress/internal/logutil"
	xslices "github.com/frantjc/x/slices"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
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
	client.Client
	record.EventRecorder
	*rest.Config
	*kubernetes.Clientset
	Portforward bool

	svcKeyToForwardAddr sync.Map
	close               func() error
}

// +kubebuilder:rbac:groups=networking/v1,resources=ingresses,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking/v1,resources=ingresses/finalizers,verbs=update
// +kubebuilder:rbac:groups=networking/v1,resources=ingresses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=pods;secrets;services,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods/portforwards,verbs=create

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *IngressReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var (
		log = logutil.SloggerFrom(ctx)
	)
	// Just populating the client cache is sufficient for now.
	log.Info(req.String())

	return ctrl.Result{}, nil
}

func (r *IngressReconciler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var (
		ctx     = req.Context()
		log     = logutil.SloggerFrom(ctx).With("action", "ServeHTTP", "host", req.Host, "path", req.URL.Path)
		ingList = &networkingv1.IngressList{}
	)
	log.Info("serving")

	if err := r.List(ctx, ingList); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	paths := []ingress.Path{}

	for _, ing := range ingList.Items {
		_log := log.With("ingress", fmt.Sprintf("%s/%s", ing.Namespace, ing.Name))

		ingRule := xslices.Find(ing.Spec.Rules, func(ingRule networkingv1.IngressRule, _ int) bool {
			return ingRule.Host == req.Host
		})

		if ingRule.HTTP == nil {
			continue
		}

		_log.Debug("found matching rule")

		for _, ingPath := range ingRule.HTTP.Paths {
			if ingPath.PathType == nil {
				continue
			}

			handler, err := r.handlerForBackend(logutil.SloggerInto(ctx, _log), ing.Namespace, ingPath.Backend)
			if err != nil {
				_log.Error(err.Error())
				continue
			}

			switch *ingPath.PathType {
			case networkingv1.PathTypeExact:
				paths = append(paths, ingress.ExactPath(ingPath.Path, handler))
			case networkingv1.PathTypePrefix, networkingv1.PathTypeImplementationSpecific:
				paths = append(paths, ingress.PrefixPath(ingPath.Path, handler))
			}
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
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.Ingress{}).
		Complete(r)
}

func (r *IngressReconciler) handlerForBackend(ctx context.Context, namespace string, ingressBackend networkingv1.IngressBackend) (http.Handler, error) {
	if ingressBackend.Service != nil {
		return r.handlerForService(ctx, namespace, *ingressBackend.Service)
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

	targetPort := svcPort.TargetPort.String()

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
			log    = log.With("pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name), "targetPort", targetPort)
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
			stopC  = make(chan struct{})
			readyC = make(chan struct{}, 1)
		)
		if r.close == nil {
			r.close = func() error {
				return nil
			}
		}
		r.close = func() error {
			close(stopC)
			return r.close()
		}
		log.Debug("portforwarding")

		portforwarder, err := portforward.New(
			dialer,
			// Choose any available port--this is ephemeral, and we can get it back from portforwarder.GetPorts().
			[]string{fmt.Sprintf(":%s", targetPort)},
			stopC, readyC,
			// TODO(frantjc): Direct these to log.Debug and log.Error, respectively.
			io.Discard, io.Discard,
		)
		if err != nil {
			return nil, err
		}
		r.close = func() error {
			portforwarder.Close()
			return r.close()
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
	return nil
}
