package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/frantjc/go-ingress/api/v1alpha1"
	"github.com/frantjc/go-ingress/internal/controller"
	"github.com/frantjc/go-ingress/internal/logutil"
	xerrors "github.com/frantjc/x/errors"
	xos "github.com/frantjc/x/os"
	xslices "github.com/frantjc/x/slices"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"

	// Registers --kubeconfig flag on flag.Commandline.
	"sigs.k8s.io/controller-runtime/pkg/client"
	_ "sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	err := xerrors.Ignore(newManager().ExecuteContext(ctx), context.Canceled)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
	}

	stop()
	xos.ExitFromError(err)
}

// +kubebuilder:rbac:groups="",resources=services,verbs=get

func newManager() *cobra.Command {
	var (
		httpAddr             string
		httpsAddr            string
		metricsAddr          string
		probeAddr            string
		enableLeaderElection bool
		slogConfig           = new(logutil.SlogConfig)
		reconciler           = new(controller.IngressReconciler)
		rawLoadBalancer      string
		cmd                  = &cobra.Command{
			Use:           "manager",
			Version:       SemVer(),
			SilenceErrors: true,
			SilenceUsage:  true,
			RunE: func(cmd *cobra.Command, _ []string) error {
				var (
					slogHandler = slog.NewTextHandler(cmd.OutOrStdout(), &slog.HandlerOptions{
						Level: slogConfig,
					})
					log  = slog.New(slogHandler)
					logr = logr.FromSlogHandler(slogHandler)
					ctx  = logutil.SloggerInto(cmd.Context(), log)
				)
				ctrl.SetLogger(logr)

				cfg, err := ctrl.GetConfig()
				if err != nil {
					return err
				}

				loadBalancer, err := url.Parse(rawLoadBalancer)
				if err != nil {
					return err
				}

				var (
					tlsOpts = []func(*tls.Config){
						func(c *tls.Config) {
							c.NextProtos = []string{"http/1.1"}
						},
					}
					webhookServer = webhook.NewServer(webhook.Options{
						TLSOpts: tlsOpts,
					})
					metricsServerOptions = server.Options{
						BindAddress: metricsAddr,
						TLSOpts:     tlsOpts,
					}
				)

				scheme := runtime.NewScheme()

				if err := networkingv1.AddToScheme(scheme); err != nil {
					return err
				}

				if err := corev1.AddToScheme(scheme); err != nil {
					return err
				}

				if err := v1alpha1.AddToScheme(scheme); err != nil {
					return err
				}

				mgr, err := ctrl.NewManager(cfg, ctrl.Options{
					Scheme:                        scheme,
					Metrics:                       metricsServerOptions,
					WebhookServer:                 webhookServer,
					HealthProbeBindAddress:        probeAddr,
					LeaderElection:                enableLeaderElection,
					LeaderElectionID:              "a0c5560e.go.ingress.kubernetes.io",
					LeaderElectionReleaseOnCancel: true,
					Logger:                        logr,
				})
				if err != nil {
					return err
				}

				switch loadBalancer.Scheme {
				case "raw", "":
					reconciler.GetIngressLoadBalancerIngress = func(_ context.Context) (*networkingv1.IngressLoadBalancerIngress, error) {
						ports := []networkingv1.IngressPortStatus{}
						if port, err := strconv.Atoi(loadBalancer.Port()); err == nil {
							ports = append(ports, networkingv1.IngressPortStatus{
								Port:     int32(port),
								Protocol: "tcp",
							})
						}
						if net.ParseIP(loadBalancer.Hostname()) != nil {
							return &networkingv1.IngressLoadBalancerIngress{
								IP:    loadBalancer.Hostname(),
								Ports: ports,
							}, nil
						}
						return &networkingv1.IngressLoadBalancerIngress{
							Hostname: loadBalancer.Hostname(),
							Ports:    ports,
						}, nil
					}
				case "service", "svc":
					reconciler.GetIngressLoadBalancerIngress = func(ctx context.Context) (*networkingv1.IngressLoadBalancerIngress, error) {
						svc := &corev1.Service{}

						if err := mgr.GetClient().Get(ctx, client.ObjectKey{Namespace: loadBalancer.Host, Name: strings.TrimPrefix(loadBalancer.Path, "/")}, svc); err != nil {
							return nil, err
						}

						for _, loadBalancerIngress := range svc.Status.LoadBalancer.Ingress {
							return &networkingv1.IngressLoadBalancerIngress{
								IP:       loadBalancerIngress.IP,
								Hostname: loadBalancerIngress.Hostname,
								Ports: xslices.Map(loadBalancerIngress.Ports, func(port corev1.PortStatus, _ int) networkingv1.IngressPortStatus {
									return networkingv1.IngressPortStatus(port)
								}),
							}, nil
						}

						return nil, fmt.Errorf("unable to get load balancer ingress from %s", loadBalancer)
					}
				}

				if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
					return err
				}

				if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
					return err
				}

				if err := reconciler.SetupWithManager(mgr); err != nil {
					return err
				}
				defer reconciler.Close()

				var (
					srv = &http.Server{
						ReadHeaderTimeout: time.Second * 5,
						BaseContext: func(_ net.Listener) context.Context {
							return ctx
						},
						ErrorLog: slog.NewLogLogger(log.Handler(), slog.LevelError),
						Handler:  reconciler,
					}
					tlsConfig = &tls.Config{
						GetCertificate: reconciler.GetCertificate,
					}
				)
				eg, ctx := errgroup.WithContext(ctx)

				eg.Go(func() error {
					return mgr.Start(ctx)
				})

				httpsLis, err := net.Listen("tcp", httpsAddr)
				if err != nil {
					return err
				}
				defer httpsLis.Close()

				eg.Go(func() error {
					return srv.Serve(tls.NewListener(httpsLis, tlsConfig))
				})

				httpLis, err := net.Listen("tcp", httpAddr)
				if err != nil {
					return err
				}
				defer httpLis.Close()

				eg.Go(func() error {
					return srv.Serve(httpLis)
				})

				eg.Go(func() error {
					<-ctx.Done()
					if err = srv.Shutdown(context.WithoutCancel(ctx)); err != nil {
						return err
					}
					return ctx.Err()
				})

				return eg.Wait()
			},
		}
	)

	cmd.Flags().BoolP("help", "h", false, "Help for "+cmd.Name())
	cmd.Flags().Bool("version", false, "Version for "+cmd.Name())
	cmd.SetVersionTemplate("{{ .Name }}{{ .Version }}")
	slogConfig.AddFlags(cmd.Flags())

	// Allow the --kubeconfig flag, which is consumed by sigs.k8s.io/controller-runtime when we call ctrl.GetConfig().
	cmd.Flags().AddGoFlagSet(flag.CommandLine)
	cmd.Flags().StringVar(&metricsAddr, "metrics-addr", "127.0.0.1:8081", "Metrics server bind address")
	cmd.Flags().StringVar(&probeAddr, "probe-addr", "127.0.0.1:8082", "Probe server bind address")
	cmd.Flags().BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election for controller manager")

	cmd.Flags().StringVar(&httpsAddr, "https-addr", ":8443", "Ingress server https bind address")
	cmd.Flags().StringVar(&httpAddr, "http-addr", ":8080", "Ingress server http bind address")
	cmd.Flags().BoolVar(&reconciler.Portforward, "port-forward", false, "Portforward to Pods")
	cmd.Flags().StringVar(&reconciler.IngressClassName, "ingress-class-name", "go-ingress", "IngressClass name")
	cmd.Flags().StringVar(&rawLoadBalancer, "load-balancer", "", "LoadBalancer address")
	cmd.MarkFlagRequired("load-balancer")

	return cmd
}
