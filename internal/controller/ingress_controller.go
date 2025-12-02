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
	PortForward bool
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
	// Just populating the client cache is sufficient.
	log.Info(req.String())

	return ctrl.Result{}, nil
}

func (r *IngressReconciler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var (
		ctx         = req.Context()
		log         = logutil.SloggerFrom(ctx).With("action", "ServeHTTP", "host", req.Host, "path", req.URL.Path)
		ingList = &networkingv1.IngressList{}
	)
	log.Info("serving")

	if err := r.List(ctx, ingList); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	paths := []ingress.Path{}

	for _, ing := range ingList.Items {
		ingLog := log.With("ingress", fmt.Sprintf("%s/%s", ing.Namespace, ing.Name))

		ingRule := xslices.Find(ing.Spec.Rules, func(ingRule networkingv1.IngressRule, _ int) bool {
			log.Debug("comparing rule", "host", ingRule.Host)
			return ingRule.Host == req.Host
		})

		if ingRule.HTTP == nil {
			continue
		}

		ingLog.Debug("found matching rule")

		for _, ingPath := range ingRule.HTTP.Paths {
			if ingPath.PathType == nil {
				continue
			}

			if ingPath.Backend.Service == nil {
				continue
			}

			var (
				svcName = ingPath.Backend.Service.Name
				tgtPort = fmt.Sprint(ingPath.Backend.Service.Port.Number)
				svcKey  = client.ObjectKey{Namespace: ing.Namespace, Name: svcName}
				svcLog  = ingLog.With("svc", svcKey.String())
			)
			if ingPath.Backend.Service.Port.Name != "" {
				svcLog.Debug("finding service port number by name")
				svc := &corev1.Service{}

				if err := r.Get(ctx, svcKey, svc); err != nil {
					log.Error(err.Error())
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}

				svcPort := xslices.Find(svc.Spec.Ports, func(svcPort corev1.ServicePort, _ int) bool {
					return svcPort.Name == svcName
				})

				tgtPort = fmt.Sprint(svcPort.Port)
			}

			reverseProxy := httputil.NewSingleHostReverseProxy(&url.URL{
				Scheme: "http",
				Host: fmt.Sprintf("%s.%s.svc.cluster.local:%s", svcName, ing.Namespace, tgtPort),
			})
			reverseProxy.ErrorLog = slog.NewLogLogger(log.Handler(), slog.LevelError)
			var handler http.Handler = reverseProxy

			if r.PortForward {
				var (
					podList = &corev1.PodList{}
					svc     = &corev1.Service{}
				)

				if err := r.Get(ctx, svcKey, svc); err != nil {
					svcLog.Error(err.Error())
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}

				svcPort := xslices.Find(svc.Spec.Ports, func(svcPort corev1.ServicePort, _ int) bool {
					return svcPort.Name == svcName
				})

				tgtPort = svcPort.TargetPort.String()

				if err := r.Client.List(ctx, podList, &client.ListOptions{
					Namespace: ing.Namespace,
					LabelSelector: labels.SelectorFromSet(svc.Spec.Selector),
				}); err != nil {
					svcLog.Error(err.Error())
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}

				roundTripper, upgrader, err := spdy.RoundTripperFor(r.Config)
				if err != nil {
					svcLog.Error(err.Error())
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}

				for _, pod := range podList.Items {
					var (
						podLog = svcLog.With("pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name))
						dialer = spdy.NewDialer(
							upgrader,
							&http.Client{Transport: roundTripper},
							http.MethodPost,
							r.CoreV1().RESTClient().
								Post().
								Resource("pods").
								Namespace(pod.Namespace).
								Name(pod.Name).
								SubResource("portforward").
								URL(),
						)
						stopC = make(chan struct{}, 1)
						readyC = make(chan struct{}, 1)
					)
					podLog.Debug("portforwarding")

					pf, err := portforward.New(dialer, []string{fmt.Sprintf(":%s", tgtPort)}, stopC, readyC, io.Discard, io.Discard)
					if err != nil {
						podLog.Error(err.Error())
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}
					defer pf.Close()

					fp, err := pf.GetPorts()
					if err != nil {
						podLog.Error(err.Error())
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}

					pf.ForwardPorts()
				}
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
		// TODO(frantjc): Find a way to set chi.ctx.
		ctx       = chi.Context()
		log       = slog.New(logr.ToSlogHandler(ctrl.Log)).With("serverName", chi.ServerName, "action", "GetCertificate")
		ingList   = &networkingv1.IngressList{}
	)

	if err := r.List(ctx, ingList); err != nil {
		return nil, err
	}

	for _, ing := range ingList.Items {
		ingLog := log.With("ingress", fmt.Sprintf("%s/%s", ing.Namespace, ing.Name))

		if xslices.Some(ing.Spec.Rules, func(ingressRule networkingv1.IngressRule, _ int) bool {
			return ingressRule.Host == chi.ServerName
		}) {
			ingLog.Debug("found matching rule")

			ingTLS := xslices.Find(ing.Spec.TLS, func(ingTLS networkingv1.IngressTLS, _ int) bool {
				return slices.Contains(ingTLS.Hosts, chi.ServerName)
			})

			tlsLog := ingLog.With("tlsSecret", fmt.Sprintf("%s/%s", ing.Namespace, ingTLS.SecretName))

			if ingTLS.SecretName != "" {				
				tlsLog.Debug("found matching tls")

				sec := &corev1.Secret{}

				if err := r.Get(ctx, client.ObjectKey{Namespace: ing.Namespace, Name: ingTLS.SecretName}, sec); err != nil {
					tlsLog.Error(err.Error())
					return nil, err
				}

				crt, err := tls.X509KeyPair(sec.Data["tls.crt"], sec.Data["tls.key"])
				if err != nil {
					tlsLog.Error(err.Error())
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
