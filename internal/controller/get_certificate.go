package controller

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"slices"

	xslices "github.com/frantjc/x/slices"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (c *IngressController) GetCertificate(chi *tls.ClientHelloInfo) (*tls.Certificate, error) {
	var (
		// TODO(frantjc): Find a way to set chi.ctx so that we can use our context-propagated logger.
		ctx     = chi.Context()
		log     = slog.New(logr.ToSlogHandler(ctrl.Log)).With("serverName", chi.ServerName, "action", "GetCertificate")
		ingList = &networkingv1.IngressList{}
	)

	if err := c.List(ctx, ingList); err != nil {
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

				tlsSecret := &corev1.Secret{}

				if err := c.Get(ctx, client.ObjectKey{Namespace: ing.Namespace, Name: ingTLS.SecretName}, tlsSecret); err != nil {
					_log.Error(err.Error())
					return nil, err
				}

				crt, err := tls.X509KeyPair(tlsSecret.Data["tls.crt"], tlsSecret.Data["tls.key"])
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
