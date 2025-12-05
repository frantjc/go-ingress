package controller

import (
	"context"
	"log/slog"
	"strconv"
	"sync"

	"github.com/go-logr/logr"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// IngressController validates and reconciles a Ingress object
type IngressController struct {
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

// +kubebuilder:rbac:groups=networking/v1,resources=ingresses,verbs=get;list;watch;update
// +kubebuilder:rbac:groups=networking/v1,resources=ingressclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking/v1,resources=ingresses/status,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (c *IngressController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var (
		log = slog.New(logr.ToSlogHandler(ctrl.LoggerFrom(ctx))).With("action", "Reconcile")
		ing = &networkingv1.Ingress{}
	)
	log.Info(req.String())

	if err := c.Get(ctx, req.NamespacedName, ing); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, err
	}

	ingressLoadBalancerIngress, err := c.GetIngressLoadBalancerIngress(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	if ing.Spec.IngressClassName != nil {
		if *ing.Spec.IngressClassName != c.IngressClassName {
			return ctrl.Result{}, nil
		}
	} else {
		if ing.Annotations != nil {
			if ingClassName, ok := ing.Annotations["kubernetes.io/ingress.class"]; ok {
				if ingClassName != c.IngressClassName {
					return ctrl.Result{}, nil
				}

				ing.Spec.IngressClassName = &ingClassName
				if err := c.Update(ctx, ing); err != nil {
					return ctrl.Result{}, err
				}

				return ctrl.Result{}, nil
			}
		}

		ingClass := &networkingv1.IngressClass{}

		if err := c.Get(ctx, client.ObjectKey{Name: c.IngressClassName}, ingClass); err != nil {
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

		ing.Spec.IngressClassName = &c.IngressClassName
		if err := c.Update(ctx, ing); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	ing.Status.LoadBalancer.Ingress = []networkingv1.IngressLoadBalancerIngress{*ingressLoadBalancerIngress}

	if err := c.Status().Update(ctx, ing); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (c *IngressController) SetupWithManager(mgr ctrl.Manager) (err error) {
	c.Client = mgr.GetClient()
	c.EventRecorder = mgr.GetEventRecorderFor("ingresses")
	c.Config = mgr.GetConfig()
	if c.Clientset, err = kubernetes.NewForConfig(c.Config); err != nil {
		return err
	}
	if err := ctrl.NewWebhookManagedBy(mgr).
		WithValidator(c).
		Complete(); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.Ingress{}).
		Complete(c)
}
