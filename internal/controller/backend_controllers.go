package controller

import (
	"context"
	"fmt"
	"net/url"
	"slices"

	"github.com/frantjc/go-ingress/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// +kubebuilder:webhook:mutating=false,path=/validate-backend-ingress-frantj-cc-v1alpha1-proxy,failurePolicy=fail,sideEffects=None,groups=backend.ingress.frantj.cc,resources=proxy,verbs=create;update,versions=v1alpha1,name=proxy.backend.ingress.frantj.cc,admissionReviewVersions=v1,serviceNamespace=go-ingress,serviceName=go-ingress

// ProxyController validates a Proxy object
type ProxyController struct{}

func (c *ProxyController) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	proxy, ok := obj.(*v1alpha1.Proxy)
	if !ok {
		return admission.Warnings{}, nil
	}

	if u, err := url.Parse(proxy.Spec.URL); err != nil {
		return nil, err
	} else if !slices.Contains([]string{"http", "https"}, u.Scheme) {
		return nil, fmt.Errorf("cannot proxy to scheme %s", u.Scheme)
	}

	return admission.Warnings{}, nil
}

func (c *ProxyController) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	return c.ValidateCreate(ctx, newObj)
}

func (c *ProxyController) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return admission.Warnings{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (c *ProxyController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		WithValidator(c).
		Complete()
}

// +kubebuilder:webhook:mutating=false,path=/validate-backend-ingress-frantj-cc-v1alpha1-redirect,failurePolicy=fail,sideEffects=None,groups=backend.ingress.frantj.cc,resources=redirect,verbs=create;update,versions=v1alpha1,name=redirect.backend.ingress.frantj.cc,admissionReviewVersions=v1,serviceNamespace=go-ingress,serviceName=go-ingress

// RedirectController validates a Redirect object
type RedirectController struct{}

func (c *RedirectController) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	redirect, ok := obj.(*v1alpha1.Redirect)
	if !ok {
		return admission.Warnings{}, nil
	}

	if u, err := url.Parse(redirect.Spec.URL); err != nil {
		return nil, err
	} else if !slices.Contains([]string{"http", "https"}, u.Scheme) {
		return nil, fmt.Errorf("cannot redirect to scheme %s", u.Scheme)
	}

	return admission.Warnings{}, nil
}

func (c *RedirectController) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	return c.ValidateCreate(ctx, newObj)
}

func (c *RedirectController) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return admission.Warnings{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (c *RedirectController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		WithValidator(c).
		Complete()
}

// +kubebuilder:webhook:mutating=false,path=/validate-backend-ingress-frantj-cc-v1alpha1-basicauth,failurePolicy=fail,sideEffects=None,groups=backend.ingress.frantj.cc,resources=basicauth,verbs=create;update,versions=v1alpha1,name=basicauth.backend.ingress.frantj.cc,admissionReviewVersions=v1,serviceNamespace=go-ingress,serviceName=go-ingress

// BasicAuthController validates a BasicAuth object
type BasicAuthController struct{}

func (c *BasicAuthController) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	basicAuth, ok := obj.(*v1alpha1.BasicAuth)
	if !ok {
		return admission.Warnings{}, nil
	}

	if basicAuth.Spec.SecretKeyRef.Optional != nil && *basicAuth.Spec.SecretKeyRef.Optional {
		return admission.Warnings{}, fmt.Errorf("secret key is required")
	}

	if warnings, err := validateBackend(&basicAuth.Spec.Backend); err != nil {
		return warnings, err
	} else if basicAuth.Spec.Backend.Resource != nil && basicAuth.Spec.Backend.Resource.Kind == "BasicAuth" {
		return admission.Warnings{}, fmt.Errorf("cannot use another basicauth as a basicauth backend")
	}

	return admission.Warnings{}, nil
}

func (c *BasicAuthController) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	return c.ValidateCreate(ctx, newObj)
}

func (c *BasicAuthController) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return admission.Warnings{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (c *BasicAuthController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		WithValidator(c).
		Complete()
}
