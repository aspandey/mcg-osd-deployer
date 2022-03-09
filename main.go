/*
Copyright 2022.

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
	"context"
	"flag"
	"fmt"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"github.com/go-logr/logr"
	odfv1alpha1 "github.com/red-hat-data-services/odf-operator/api/v1alpha1"
	ocsv1 "github.com/red-hat-storage/ocs-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	noobaa "github.com/noobaa/noobaa-operator/v5/pkg/apis"
	operatorv1 "github.com/openshift/api/operator/v1"
	opv1a1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	mcgv1alpha1 "github.com/red-hat-storage/mcg-osd-deployer/api/v1alpha1"
	"github.com/red-hat-storage/mcg-osd-deployer/controllers"

	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	promv1a1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1alpha1"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

const (
	namespaceEnvVarName = "NAMESPACE"
	addonNameEnvVarName = "ADDON_NAME"
)

func init() {
	addAllSchemes(scheme)
	//+kubebuilder:scaffold:scheme
}

func addAllSchemes(scheme *runtime.Scheme) {

	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(ocsv1.AddToScheme(scheme))

	utilruntime.Must(odfv1alpha1.AddToScheme(scheme))

	utilruntime.Must(mcgv1alpha1.AddToScheme(scheme))

	utilruntime.Must(noobaa.AddToScheme(scheme))

	utilruntime.Must(opv1a1.AddToScheme(scheme))

	utilruntime.Must(operatorv1.AddToScheme(scheme))

	utilruntime.Must(promv1.AddToScheme(scheme))

	utilruntime.Must(promv1a1.AddToScheme(scheme))

	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")

	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	envVars, err := readEnvVars()
	if err != nil {
		setupLog.Error(err, "Failed to get environment variables.")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: metricsAddr,
		Port:               9443,
		LeaderElection:     enableLeaderElection,
		LeaderElectionID:   "af4bf43b.mcg.openshift.io",
		Namespace:          envVars[namespaceEnvVarName],
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager.")
		os.Exit(1)
	}
	addonName := envVars[addonNameEnvVarName]
	if err = (&controllers.ManagedMCGReconciler{
		Client:                       mgr.GetClient(),
		UnrestrictedClient:           getUnrestrictedClient(),
		Log:                          ctrl.Log.WithName("controllers").WithName("ManagedMCG"),
		Scheme:                       mgr.GetScheme(),
		AddonConfigMapName:           addonName,
		AddonConfigMapDeleteLabelKey: fmt.Sprintf("api.openshift.com/addon-%v-delete", addonName),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ManagedMCG")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	if err := ensureManagedMCG(mgr.GetClient(), setupLog, envVars); err != nil {
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func getUnrestrictedClient() client.Client {
	var options client.Options

	options.Scheme = runtime.NewScheme()
	addAllSchemes(options.Scheme)
	k8sClient, err := client.New(config.GetConfigOrDie(), options)
	if err != nil {
		setupLog.Error(err, "error creating client")
		os.Exit(1)
	}
	return k8sClient
}

func readEnvVars() (map[string]string, error) {
	envVars := map[string]string{
		namespaceEnvVarName: "",
		addonNameEnvVarName: "",
	}
	for envVarName := range envVars {
		val, found := os.LookupEnv(envVarName)
		if !found {
			return nil, fmt.Errorf("%s environment variable must be set", envVarName)
		}
		envVars[envVarName] = val
	}

	return envVars, nil
}

func ensureManagedMCG(c client.Client, log logr.Logger, envVars map[string]string) error {
	err := c.Create(context.Background(), &mcgv1alpha1.ManagedMCG{
		ObjectMeta: metav1.ObjectMeta{
			Name:       controllers.ManagedMCGName,
			Namespace:  envVars[namespaceEnvVarName],
			Finalizers: []string{controllers.ManagedMCGFinalizer},
		},
	})
	if err == nil {
		log.Info("ManagedMCG resource created.")
		return nil

	} else if errors.IsAlreadyExists(err) {
		log.Info("ManagedMCG resource already exists.")
		return nil

	} else {
		log.Error(err, "Unable to create ManagedMCG resource")
		return err
	}
}
