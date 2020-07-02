// Copyright 2019 Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package mesh

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"istio.io/istio/operator/pkg/apis/istio/v1alpha1"
	"istio.io/istio/operator/pkg/cache"
	"istio.io/istio/operator/pkg/controller/istiocontrolplane"
	"istio.io/istio/operator/pkg/helmreconciler"
	"istio.io/istio/operator/pkg/name"
	"istio.io/istio/operator/pkg/object"
	"istio.io/istio/operator/pkg/translate"
	"istio.io/istio/operator/pkg/util"
	"istio.io/istio/operator/pkg/util/clog"
	"istio.io/pkg/log"
)

// cmdType is one of the commands used to generate and optionally apply a manifest.
type cmdType int

const (
	// istioctl manifest generate
	cmdGenerate cmdType = iota
	// istioctl manifest apply or istioctl install
	cmdApply
	// in-cluster controller
	cmdController
)

// Golden output files add a lot of noise to pull requests. Use a unique suffix so
// we can hide them by default. This should match one of the `linuguist-generated=true`
// lines in istio.io/istio/.gitattributes.
const (
	goldenFileSuffixHideChangesInReview = ".golden.yaml"
	goldenFileSuffixShowChangesInReview = ".golden-show-in-gh-pull-request.yaml"
)

var (
	// By default, tests only run with manifest generate, since it doesn't require any external fake test environment.
	testedManifestCmds = []cmdType{cmdGenerate}

	// Path to the manifests/ dir in istio root dir.
	manifestsDir string
	// A release dir with the live profiles and charts is created in this dir for tests.
	liveReleaseDir string
	// Path to the operator install base dir in the live release.
	liveInstallPackageDir string

	// Only used if kubebuilder is installed.
	testenv               *envtest.Environment
	testClient            client.Client
	testReconcileOperator *istiocontrolplane.ReconcileIstioOperator

	allNamespacedGVKs = []schema.GroupVersionKind{
		{Group: "autoscaling", Version: "v2beta1", Kind: "HorizontalPodAutoscaler"},
		{Group: "policy", Version: "v1beta1", Kind: "PodDisruptionBudget"},
		{Group: "apps", Version: "v1", Kind: "Deployment"},
		{Group: "apps", Version: "v1", Kind: "DaemonSet"},
		{Group: "", Version: "v1", Kind: "Service"},
		{Group: "", Version: "v1", Kind: "ConfigMap"},
		{Group: "", Version: "v1", Kind: "Endpoints"},
		{Group: "", Version: "v1", Kind: "PersistentVolumeClaim"},
		{Group: "", Version: "v1", Kind: "Pod"},
		{Group: "", Version: "v1", Kind: "Secret"},
		{Group: "", Version: "v1", Kind: "ServiceAccount"},
		{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "RoleBinding"},
		{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "Role"},
		{Group: "networking.istio.io", Version: "v1alpha3", Kind: "DestinationRule"},
		{Group: "networking.istio.io", Version: "v1alpha3", Kind: "EnvoyFilter"},
		{Group: "networking.istio.io", Version: "v1alpha3", Kind: "Gateway"},
		{Group: "networking.istio.io", Version: "v1alpha3", Kind: "VirtualService"},
		{Group: "security.istio.io", Version: "v1beta1", Kind: "PeerAuthentication"},
	}

	// ordered by which types should be deleted, first to last
	allClusterGVKs = []schema.GroupVersionKind{
		{Group: "admissionregistration.k8s.io", Version: "v1beta1", Kind: "MutatingWebhookConfiguration"},
		{Group: "admissionregistration.k8s.io", Version: "v1beta1", Kind: "ValidatingWebhookConfiguration"},
		{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRole"},
		{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRoleBinding"},
		{Group: "apiextensions.k8s.io", Version: "v1beta1", Kind: "CustomResourceDefinition"},
	}
)

// TestMain is required to create a local release package in /tmp from manifests and operator/data in the format that
// istioctl expects. It also brings up and tears down the kubebuilder test environment if it is installed.
func TestMain(m *testing.M) {
	var err error

	// If kubebuilder is installed, use that test env for apply and controller testing.
	//if kubeBuilderInstalled() {
	if true { // XXX
		testenv = &envtest.Environment{}
		testRestConfig, err = testenv.Start()
		checkExit(err)

		testK8Interface, err = kubernetes.NewForConfig(testRestConfig)
		testRestConfig.QPS = 50
		testRestConfig.Burst = 100
		checkExit(err)
		s := scheme.Scheme
		s.AddKnownTypes(v1alpha1.SchemeGroupVersion, &v1alpha1.IstioOperator{})

		testClient, err = client.New(testRestConfig, client.Options{Scheme: s})

		checkExit(err)
		// TestMode is required to not wait in the go client for resources that will never be created in the test server.
		helmreconciler.TestMode = true
		testReconcileOperator = istiocontrolplane.NewReconcileIstioOperator(testClient, testRestConfig, s)

		// Add manifest apply and controller to the list of commands to run tests against.
		testedManifestCmds = append(testedManifestCmds, cmdApply, cmdController)
	}
	defer func() {
		if kubeBuilderInstalled() {
			testenv.Stop()
		}
	}()

	flag.Parse()
	code := m.Run()
	os.Exit(code)
}

// runManifestCommands runs all given commands with the given input IOP file, flags and chartSource. It returns
// an objectSet for each cmd type.
func runManifestCommands(inFile, flags string, chartSource chartSourceType) (map[cmdType]*objectSet, error) {
	out := make(map[cmdType]*objectSet)
	for _, cmd := range testedManifestCmds {
		switch cmd {
		case cmdApply, cmdController:
			if err := cleanTestCluster(); err != nil {
				return nil, err
			}
			if err := fakeApplyExtraResources(inFile); err != nil {
				return nil, err
			}
		default:
		}

		var objs *objectSet
		var err error
		switch cmd {
		case cmdGenerate:
			m, _, err := generateManifest(inFile, flags, chartSource)
			if err != nil {
				return nil, err
			}
			objs, err = parseObjectSetFromManifest(m)
		case cmdApply:
			objs, err = fakeApplyManifest(inFile, flags, chartSource)
		case cmdController:
			objs, err = fakeControllerReconcile(inFile, chartSource)
		default:
		}
		if err != nil {
			return nil, err
		}
		out[cmd] = objs
	}

	return out, nil
}

// fakeApplyManifest runs manifest apply. It is assumed that
func fakeApplyManifest(inFile, flags string, chartSource chartSourceType) (*objectSet, error) {
	inPath := filepath.Join(testDataDir, "input", inFile+".yaml")
	manifest, err := runManifestCommand("apply", []string{inPath}, flags, chartSource)
	if err != nil {
		return nil, fmt.Errorf("error %s: %s", err, manifest)
	}
	objs, err := getAllIstioObjects()
	return NewObjectSet(objs), err
}

// fakeApplyExtraResources applies any extra resources for the given test name.
func fakeApplyExtraResources(inFile string) error {
	reconciler, err := helmreconciler.NewHelmReconciler(testClient, testRestConfig, nil, nil)
	if err != nil {
		return err
	}

	if rs, err := readFile(filepath.Join(testDataDir, "input-extra-resources", inFile+".yaml")); err == nil {
		if err := applyWithReconciler(reconciler, rs); err != nil {
			return err
		}
	}
	return nil
}

func fakeControllerReconcile(inFile string, chartSource chartSourceType) (*objectSet, error) {
	l := clog.NewDefaultLogger()
	_, iops, err := GenerateConfig(
		[]string{inFileAbsolutePath(inFile)},
		[]string{"installPackagePath=" + string(chartSource)},
		false, testRestConfig, l)
	if err != nil {
		return nil, err
	}

	crName := installedSpecCRPrefix
	if iops.Revision != "" {
		crName += "-" + iops.Revision
	}
	iop, err := translate.IOPStoIOP(iops, crName, v1alpha1.Namespace(iops))
	if err != nil {
		return nil, err
	}
	iop.Spec.InstallPackagePath = string(chartSource)

	if err := createNamespace(testK8Interface, iop.Namespace); err != nil {
		return nil, err
	}

	reconciler, err := helmreconciler.NewHelmReconciler(testClient, testRestConfig, iop, nil)
	if err != nil {
		return nil, err
	}
	if err := fakeInstallOperator(reconciler, chartSource, iop); err != nil {
		return nil, err
	}

	if _, err := reconciler.Reconcile(); err != nil {
		return nil, err
	}

	objs, err := getAllIstioObjects()
	return NewObjectSet(objs), err
}

// fakeInstallOperator installs the operator manifest resources into a cluster using the given reconciler.
// The installation is for testing with a kubebuilder fake cluster only, since no functional Deployment will be
// created.
func fakeInstallOperator(reconciler *helmreconciler.HelmReconciler, chartSource chartSourceType, iop *v1alpha1.IstioOperator) error {
	ocArgs := &operatorCommonArgs{
		manifestsPath:     string(chartSource),
		istioNamespace:    istioDefaultNamespace,
		watchedNamespaces: istioDefaultNamespace,
		operatorNamespace: operatorDefaultNamespace,
		hub:               "foo",
		tag:               "bar",
	}

	_, mstr, err := renderOperatorManifest(nil, ocArgs)
	if err != nil {
		return err
	}
	if err := applyWithReconciler(reconciler, mstr); err != nil {
		return err
	}
	iopStr, err := util.MarshalWithJSONPB(iop)
	if err != nil {
		return err
	}
	if err := saveIOPToCluster(reconciler, iopStr); err != nil {
		return err
	}

	return err
}

// applyWithReconciler applies the given manifest string using the given reconciler.
func applyWithReconciler(reconciler *helmreconciler.HelmReconciler, manifest string) error {
	m := name.Manifest{
		Name:    name.IstioOperatorComponentName,
		Content: manifest,
	}
	_, _, err := reconciler.ApplyManifest(m)
	return err
}

// runManifestCommand runs the given manifest command. If filenames is set, passes the given filenames as -f flag,
// flags is passed to the command verbatim. If you set both flags and path, make sure to not use -f in flags.
func runManifestCommand(command string, filenames []string, flags string, chartSource chartSourceType) (string, error) {
	args := "manifest " + command
	for _, f := range filenames {
		args += " -f " + f
	}
	if flags != "" {
		args += " " + flags
	}
	args += " --set installPackagePath=" + string(chartSource)
	return runCommand(args)
}

// runCommand runs the given command string.
func runCommand(command string) (string, error) {
	var out bytes.Buffer
	rootCmd := GetRootCmd(strings.Split(command, " "))
	rootCmd.SetOut(&out)

	err := rootCmd.Execute()
	return out.String(), err
}

func cleanTestCluster() error {
	reconciler, err := helmreconciler.NewHelmReconciler(testClient, testRestConfig, nil, nil)
	if err != nil {
		return err
	}
	// Needed in case we are running a test through this path that doesn't start a new process.
	cache.FlushObjectCaches()
	return reconciler.DeleteAll()
}

// getAllIstioObjects lists all Istio GVK resources from the testClient.
func getAllIstioObjects() (object.K8sObjects, error) {
	var out object.K8sObjects
	for _, gvk := range append(allClusterGVKs, allNamespacedGVKs...) {
		objects := &unstructured.UnstructuredList{}
		objects.SetGroupVersionKind(gvk)
		if err := testClient.List(context.TODO(), objects); err != nil {
			log.Error(err.Error())
			continue
		}
		for _, o := range objects.Items {
			no := o.DeepCopy()
			out = append(out, object.NewK8sObject(no, nil, nil))
		}
	}
	return out, nil
}

// readFile reads a file and returns the contents.
func readFile(path string) (string, error) {
	b, err := ioutil.ReadFile(path)
	return string(b), err
}

// inFileAbsolutePath returns the absolute path for an input file like "gateways".
func inFileAbsolutePath(inFile string) string {
	return filepath.Join(testDataDir, "input", inFile+".yaml")
}
