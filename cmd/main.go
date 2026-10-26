/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
	"github.com/openconveyor/openconveyor/internal/controller"
	"github.com/openconveyor/openconveyor/internal/trigger"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

// defaultTriggerNamespace is the fallback namespace for signature
// Secret lookups and emitted Tasks when neither the flag nor
// POD_NAMESPACE is set. Matches the release manifests' namespace.
const defaultTriggerNamespace = "conveyor-system"

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(conveyorv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// flags carries every command-line option the manager understands.
type flags struct {
	metricsAddr          string
	metricsCertPath      string
	metricsCertName      string
	metricsCertKey       string
	webhookCertPath      string
	webhookCertName      string
	webhookCertKey       string
	probeAddr            string
	enableLeaderElection bool
	secureMetrics        bool
	enableHTTP2          bool
	triggerAddr          string
	triggerNamespace     string
	egressRefresh        time.Duration
}

// defaultEgressRefresh bounds how long a Task's NetworkPolicy can lag a
// DNS change. Five minutes is well under typical CDN TTL churn and keeps
// the per-Task reconcile rate negligible.
const defaultEgressRefresh = 5 * time.Minute

func parseFlags() (flags, zap.Options) {
	var fl flags
	flag.StringVar(&fl.metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&fl.probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&fl.enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&fl.secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&fl.webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	flag.StringVar(&fl.webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&fl.webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.StringVar(&fl.metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&fl.metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&fl.metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&fl.enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.StringVar(&fl.triggerAddr, "trigger-bind-address", "",
		"Address for the webhook trigger adapter (e.g. :9090). Leave blank to disable the trigger server.")
	flag.StringVar(&fl.triggerNamespace, "trigger-namespace", "",
		"Namespace where HMAC Secrets referenced by ClusterTriggerClass live. "+
			"Defaults to POD_NAMESPACE or \""+defaultTriggerNamespace+"\".")
	flag.DurationVar(&fl.egressRefresh, "egress-refresh-interval", defaultEgressRefresh,
		"How often a running Task whose egress allowlist contains hostnames is re-resolved "+
			"and its NetworkPolicy updated. 0 disables re-resolution.")

	// Production JSON logging by default; --zap-devel restores the
	// development encoder for local runs.
	opts := zap.Options{}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	return fl, opts
}

// buildTLSOpts returns the TLS options shared by the metrics and
// webhook servers.
func buildTLSOpts(fl flags) []func(*tls.Config) {
	var tlsOpts []func(*tls.Config)
	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	if !fl.enableHTTP2 {
		tlsOpts = append(tlsOpts, func(c *tls.Config) {
			setupLog.Info("Disabling HTTP/2")
			c.NextProtos = []string{"http/1.1"}
		})
	}
	return tlsOpts
}

// buildWebhookServer wires the (currently webhook-less) server the
// scaffold expects; `kubebuilder create webhook` registers into it.
func buildWebhookServer(fl flags, tlsOpts []func(*tls.Config)) webhook.Server {
	options := webhook.Options{
		TLSOpts: tlsOpts,
	}
	if len(fl.webhookCertPath) > 0 {
		setupLog.Info("Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", fl.webhookCertPath,
			"webhook-cert-name", fl.webhookCertName,
			"webhook-cert-key", fl.webhookCertKey)
		options.CertDir = fl.webhookCertPath
		options.CertName = fl.webhookCertName
		options.KeyName = fl.webhookCertKey
	}
	return webhook.NewServer(options)
}

// buildMetricsOptions configures the manager's metrics endpoint.
// More info:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.3/pkg/metrics/server
// - https://book.kubebuilder.io/reference/metrics.html
func buildMetricsOptions(fl flags, tlsOpts []func(*tls.Config)) metricsserver.Options {
	options := metricsserver.Options{
		BindAddress:   fl.metricsAddr,
		SecureServing: fl.secureMetrics,
		TLSOpts:       tlsOpts,
	}
	if fl.secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.3/pkg/metrics/filters#WithAuthenticationAndAuthorization
		options.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// If the certificate is not specified, controller-runtime generates
	// self-signed certificates for the metrics server — fine for dev, not
	// recommended for production. For cert-manager-issued certs, enable
	// [METRICS-WITH-CERTS] in config/default/kustomization.yaml.
	if len(fl.metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", fl.metricsCertPath,
			"metrics-cert-name", fl.metricsCertName,
			"metrics-cert-key", fl.metricsCertKey)
		options.CertDir = fl.metricsCertPath
		options.CertName = fl.metricsCertName
		options.KeyName = fl.metricsCertKey
	}
	return options
}

// setupTrigger registers the webhook trigger adapter when
// --trigger-bind-address is set.
func setupTrigger(mgr ctrl.Manager, fl flags) error {
	if fl.triggerAddr == "" {
		return nil
	}
	ns := resolveTriggerNamespace(fl.triggerNamespace)
	triggerServer := &trigger.Server{
		Addr: fl.triggerAddr,
		Log:  ctrl.Log.WithName("trigger"),
		Handler: &trigger.Handler{
			Client:               mgr.GetClient(),
			SecretNamespace:      ns,
			DefaultTaskNamespace: ns,
			Log:                  ctrl.Log.WithName("trigger"),
		},
	}
	if err := mgr.Add(triggerServer); err != nil {
		return fmt.Errorf("register trigger server: %w", err)
	}
	setupLog.Info("Enabled trigger adapter", "addr", fl.triggerAddr, "namespace", ns)
	return nil
}

func main() {
	if err := run(); err != nil {
		setupLog.Error(err, "Manager exited with error")
		os.Exit(1)
	}
}

func run() error {
	fl, zapOpts := parseFlags()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOpts)))

	tlsOpts := buildTLSOpts(fl)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		// The trigger adapter reads HMAC Secrets by name. Bypassing the
		// cache for Secrets keeps the manager from watching every Secret
		// in the cluster just to serve those low-volume lookups.
		Client: client.Options{
			Cache: &client.CacheOptions{
				DisableFor: []client.Object{&corev1.Secret{}},
			},
		},
		Metrics:                buildMetricsOptions(fl, tlsOpts),
		WebhookServer:          buildWebhookServer(fl, tlsOpts),
		HealthProbeBindAddress: fl.probeAddr,
		LeaderElection:         fl.enableLeaderElection,
		LeaderElectionID:       "17b16654.openconveyor.ai",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		return fmt.Errorf("start manager: %w", err)
	}

	ctx := ctrl.SetupSignalHandler()

	if err := (&controller.TaskReconciler{
		Client:                mgr.GetClient(),
		Scheme:                mgr.GetScheme(),
		EgressRefreshInterval: fl.egressRefresh,
	}).SetupWithManager(ctx, mgr); err != nil {
		return fmt.Errorf("create Task controller: %w", err)
	}
	// +kubebuilder:scaffold:builder

	if err := setupTrigger(mgr, fl); err != nil {
		return err
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return fmt.Errorf("set up health check: %w", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return fmt.Errorf("set up ready check: %w", err)
	}

	setupLog.Info("Starting manager")
	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("run manager: %w", err)
	}
	return nil
}

// resolveTriggerNamespace picks the namespace for signature Secret lookups.
// Priority: explicit flag > POD_NAMESPACE (downward API) > defaultTriggerNamespace.
func resolveTriggerNamespace(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if ns := os.Getenv("POD_NAMESPACE"); ns != "" {
		return ns
	}
	return defaultTriggerNamespace
}
