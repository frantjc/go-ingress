package controller_test

import (
	"crypto/tls"
	"testing"

	"github.com/frantjc/go-ingress/internal/controller"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func FuzzIngressController_GetCertificate_SuccessfulLookup(f *testing.F) {
	f.Add("kube-system", "test-tls", "test", "example.com")
	f.Add("go-ingress", "frantjc-tls", "frantjc", "frantj.cc")
	f.Fuzz(func(t *testing.T, namespace, secretName, ingressName, host string) {
		tlsCrt, tlsKey := generateTLSKeyPair(t, host)
		client := fake.NewFakeClient(
			&networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: ingressName},
				Spec: networkingv1.IngressSpec{
					Rules: []networkingv1.IngressRule{{Host: host}},
					TLS:   []networkingv1.IngressTLS{{Hosts: []string{host}, SecretName: secretName}},
				},
			},
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: secretName},
				Data:       map[string][]byte{"tls.crt": tlsCrt, "tls.key": tlsKey},
			},
		)
		ctrl := &controller.IngressController{Client: client}
		chi := &tls.ClientHelloInfo{ServerName: host}
		crt, err := ctrl.GetCertificate(chi)
		assert.NoError(t, err)
		assert.NotNil(t, crt)
	})
}

func FuzzIngressController_GetCertificate_NoIngressMatch(f *testing.F) {
	f.Add("example.com")
	f.Add("frantj.cc")
	f.Fuzz(func(t *testing.T, host string) {
		client := fake.NewFakeClient()
		ctrl := &controller.IngressController{Client: client}
		chi := &tls.ClientHelloInfo{ServerName: host}
		crt, err := ctrl.GetCertificate(chi)
		assert.NoError(t, err)
		assert.Nil(t, crt)
	})
}

func FuzzIngressController_GetCertificate_SecretNotFound(f *testing.F) {
	f.Add("kube-system", "test-tls", "test", "example.com")
	f.Add("go-ingress", "frantjc-tls", "frantjc", "frantj.cc")
	f.Fuzz(func(t *testing.T, namespace, secretName, ingressName, host string) {
		client := fake.NewFakeClient(
			&networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: ingressName},
				Spec: networkingv1.IngressSpec{
					Rules: []networkingv1.IngressRule{{Host: host}},
					TLS:   []networkingv1.IngressTLS{{Hosts: []string{host}, SecretName: secretName}},
				},
			},
		)
		ctrl := &controller.IngressController{Client: client}
		chi := &tls.ClientHelloInfo{ServerName: host}
		crt, err := ctrl.GetCertificate(chi)
		assert.Error(t, err)
		assert.Nil(t, crt)
	})
}

func FuzzIngressController_GetCertificate_NoTLSConfigured(f *testing.F) {
	f.Add("kube-system", "test", "example.com")
	f.Add("go-ingress", "frantjc", "frantj.cc")
	f.Fuzz(func(t *testing.T, namespace, ingressName, host string) {
		client := fake.NewFakeClient(
			&networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: ingressName},
				Spec: networkingv1.IngressSpec{
					Rules: []networkingv1.IngressRule{{Host: host}},
				},
			},
		)
		ctrl := &controller.IngressController{Client: client}
		chi := &tls.ClientHelloInfo{ServerName: host}
		crt, err := ctrl.GetCertificate(chi)
		assert.NoError(t, err)
		assert.Nil(t, crt)
	})
}
