package kopper

import (
	"fmt"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	"github.com/go-logr/logr"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	ctrlMetrics "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
}

type ManagerOptions struct {
	LeaderElectionID string
	AddToSchemeFunc  func(*runtime.Scheme) error
}

func Manager(opts *ManagerOptions) (manager.Manager, error) {
	if opts == nil {
		opts = &ManagerOptions{}
	}

	utilruntime.Must(opts.AddToSchemeFunc(scheme))

	logf.SetLogger(logr.Discard())
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:           scheme,
		LeaderElection:   len(opts.LeaderElectionID) > 0,
		LeaderElectionID: opts.LeaderElectionID,
		Logger:           logr.Discard(),
		Metrics: ctrlMetrics.Options{
			BindAddress: "0",
		},
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
		return nil, fmt.Errorf("error setting up manager: %w", err)
	}

	return mgr, nil
}
