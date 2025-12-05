package controller

import (
	"context"
	"fmt"

	"github.com/frantjc/go-ingress/api/v1alpha1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// +kubebuilder:webhookconfiguration:mutating=false,name=ingress.frantj.cc
// +kubebuilder:webhook:mutating=false,path=/validate-networking-v1-ingress,failurePolicy=fail,sideEffects=None,groups=networking,resources=ingress,verbs=create;update,versions=v1,name=ingress.frantj.cc,admissionReviewVersions=v1,serviceNamespace=go-ingress,serviceName=go-ingress

func (c *IngressController) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	ing, ok := obj.(*networkingv1.Ingress)
	if !ok {
		return admission.Warnings{}, nil
	}

	if ing.Spec.IngressClassName == nil || *ing.Spec.IngressClassName != c.IngressClassName {
		return admission.Warnings{}, nil
	}

	if warnings, err := validateBackend(ing.Spec.DefaultBackend); err != nil {
		return warnings, err
	}

	for _, rule := range ing.Spec.Rules {
		if rule.HTTP == nil {
			continue
		}

		for _, path := range rule.HTTP.Paths {
			if warnings, err := validateBackend(&path.Backend); err != nil {
				return warnings, err
			}
		}
	}

	return admission.Warnings{}, nil
}

func validateBackend(backend *networkingv1.IngressBackend) (admission.Warnings, error) {
	if backend == nil {
		return admission.Warnings{}, nil
	} else if backend.Service != nil {
		return admission.Warnings{}, nil
	} else if backend.Resource == nil {
		return nil, fmt.Errorf("backend must have a service or resource")
	} else if backend.Resource.APIGroup == nil {
		return nil, fmt.Errorf("backend resource must have an apiGroup")
	} else {
		switch *backend.Resource.APIGroup {
		case v1alpha1.GroupVersion.Group:
			switch backend.Resource.Kind {
			case "BasicAuth", "Proxy", "Redirect":
			default:
				return nil, fmt.Errorf("unsupported backend resource kind %s", backend.Resource.Kind)
			}
		default:
			return nil, fmt.Errorf("%s is the only supported backend resource apiGroup", v1alpha1.GroupVersion.Group)
		}
	}

	return admission.Warnings{}, nil
}

func (c *IngressController) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	return c.ValidateCreate(ctx, newObj)
}

func (c *IngressController) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return admission.Warnings{}, nil
}
