package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/frantjc/go-ingress/internal/controller"
	"github.com/frantjc/go-ingress/internal/logutil"
	xerrors "github.com/frantjc/x/errors"
	xos "github.com/frantjc/x/os"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"

	// Registers --kubeconfig flag on flag.Commandline.
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

func newManager() *cobra.Command {
	var (
		addr                 string
		metricsAddr          string
		probeAddr            string
		enableLeaderElection bool
		slogConfig           = new(logutil.SlogConfig)
		reconciler           = new(controller.IngressReconciler)
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

				lis, err := net.Listen("tcp", addr)
				if err != nil {
					return err
				}
				defer lis.Close()

				eg.Go(func() error {
					<-ctx.Done()
					if err = srv.Shutdown(context.WithoutCancel(ctx)); err != nil {
						return err
					}
					return ctx.Err()
				})

				eg.Go(func() error {
					return srv.Serve(tls.NewListener(lis, tlsConfig))
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

	cmd.Flags().StringVar(&addr, "addr", ":8080", "Ingress server bind address")
	cmd.Flags().BoolVar(&reconciler.Portforward, "port-forward", false, "Portforward to Pods")

	return cmd
}
