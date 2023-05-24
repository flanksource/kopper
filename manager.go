package kopper

import (
	"fmt"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/flanksource/commons/collections"
	missioncontrolv1 "github.com/flanksource/kopper/api/v1"
	"github.com/flanksource/kopper/controllers"
	//+kubebuilder:scaffold:imports
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(missioncontrolv1.AddToScheme(scheme))
}

type ManagerOptions struct {
	MetricsBindAddress     string
	LeaderElectionID       string
	Reconcilers            []string
	ConnectionOnUpsertFunc func(missioncontrolv1.Connection) error
	ConnectionOnDeleteFunc func(string) error
}

func Manager(opts *ManagerOptions) (manager.Manager, error) {
	if opts == nil {
		opts = &ManagerOptions{}
	}
	// Use options for reconciling setup
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: opts.MetricsBindAddress,
		Port:               9443,
		LeaderElection:     len(opts.LeaderElectionID) > 0,
		LeaderElectionID:   opts.LeaderElectionID,
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

	if len(opts.Reconcilers) == 0 {
		return nil, fmt.Errorf("no reconcilers given")
	}

	if collections.Contains(opts.Reconcilers, "Connection") {
		if err = (&controllers.ConnectionReconciler{
			Client:       mgr.GetClient(),
			Scheme:       mgr.GetScheme(),
			OnUpsertFunc: opts.ConnectionOnUpsertFunc,
			OnDeleteFunc: opts.ConnectionOnDeleteFunc,
		}).SetupWithManager(mgr); err != nil {
			return nil, fmt.Errorf("unable to create controller for Connection: %v", err)
		}
	}

	return mgr, nil
}
