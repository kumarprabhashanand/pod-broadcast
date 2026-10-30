package main

import (
	"flag"
	"fmt"
	"os"
	goruntime "runtime"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/kumarprabhashanand/pod-broadcast/api/v1alpha1"
	"github.com/kumarprabhashanand/pod-broadcast/internal/controller"
	"github.com/kumarprabhashanand/pod-broadcast/internal/transport"
	"github.com/kumarprabhashanand/pod-broadcast/internal/version"
)

func main() {
	var namespace, metricsAddr, probeAddr string
	var requirePolicy, leaderElect, showVersion bool
	var defaultTTL, maxDuration time.Duration
	flag.StringVar(&namespace, "namespace", "", "Run broadcasts in this namespace only; empty means all namespaces")
	flag.BoolVar(&requirePolicy, "require-policy", true, "Reject broadcasts in namespaces without a BroadcastPolicy")
	flag.DurationVar(&defaultTTL, "default-ttl", time.Hour, "How long finished broadcasts are kept when they don't set ttlSecondsAfterFinished")
	flag.DurationVar(&maxDuration, "max-duration", 24*time.Hour, "Longest Continuous broadcast when no policy sets maxDuration")
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "Address of the Prometheus metrics endpoint")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "Address of the health probes")
	flag.BoolVar(&leaderElect, "leader-elect", true, "Use leader election, so extra replicas wait as standbys")
	flag.BoolVar(&showVersion, "version", false, "Print the version and exit")
	flag.Parse()
	if showVersion {
		fmt.Printf("pod-broadcast-controller %s (%s %s/%s)\n", version.String(), goruntime.Version(), goruntime.GOOS, goruntime.GOARCH)
		return
	}
	ctrl.SetLogger(zap.New())
	log := ctrl.Log.WithName("setup")
	log.Info("starting", "version", version.String())

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	// The cache holds every pod the controller may target; managed fields are dead weight there.
	cacheOpts := cache.Options{DefaultTransform: cache.TransformStripManagedFields()}
	if namespace != "" {
		cacheOpts.DefaultNamespaces = map[string]cache.Config{namespace: {}}
	}
	opts := ctrl.Options{
		Scheme:                 scheme,
		Cache:                  cacheOpts,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         leaderElect,
		LeaderElectionID:       "pod-broadcast-controller",
		// The process exits right after the manager stops, so a standby can take over at once.
		LeaderElectionReleaseOnCancel: true,
	}
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), opts)
	if err != nil {
		log.Error(err, "creating the manager")
		os.Exit(1)
	}
	r := &controller.BroadcastReconciler{
		Client:        mgr.GetClient(),
		APIReader:     mgr.GetAPIReader(),
		Events:        mgr.GetEventRecorder("pod-broadcast"),
		Transport:     transport.NewDirect(),
		RequirePolicy: requirePolicy,
		DefaultTTL:    defaultTTL,
		MaxDuration:   maxDuration,
	}
	if err := r.SetupWithManager(mgr); err != nil {
		log.Error(err, "setting up the controller")
		os.Exit(1)
	}
	utilruntime.Must(mgr.AddHealthzCheck("ping", healthz.Ping))
	utilruntime.Must(mgr.AddReadyzCheck("ping", healthz.Ping))
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "running the manager")
		os.Exit(1)
	}
}
