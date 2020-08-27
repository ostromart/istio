// Copyright Istio Authors
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

package bugreport

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"istio.io/istio/operator/pkg/util"
	"istio.io/istio/tools/bug-report/pkg/archive"
	cluster2 "istio.io/istio/tools/bug-report/pkg/cluster"
	"istio.io/istio/tools/bug-report/pkg/config"
	"istio.io/istio/tools/bug-report/pkg/content"
	"istio.io/istio/tools/bug-report/pkg/filter"
	"istio.io/istio/tools/bug-report/pkg/kubeclient"
	"istio.io/istio/tools/bug-report/pkg/kubectlcmd"
	"istio.io/istio/tools/bug-report/pkg/processlog"
	"istio.io/pkg/log"
	"istio.io/pkg/version"
)

const (
	bugReportDefaultMaxSizeMb = 500
	bugReportDefaultTimeout   = 30 * time.Minute
	bugReportDefaultTempDir   = "/tmp/bug-report"
)

var (
	bugReportDefaultIstioNamespace = "istio-system"
	bugReportDefaultInclude        = []string{""}
	bugReportDefaultExclude        = []string{"kube-system,kube-public"}
)

// BugReportCmd returns a cobra command for bug-report.
func BugReportCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:          "bug-report",
		Short:        "Cluster information and log capture support tool.",
		SilenceUsage: true,
		Long: "This command selectively captures cluster information and logs into an archive to help " +
			"diagnose problems. It optionally uploads the archive to a GCS bucket.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBugReportCommand(cmd)
		},
	}
	rootCmd.AddCommand(version.CobraCommand())
	addFlags(rootCmd, gConfig)

	return rootCmd
}

var (
	// Logs, along with stats and importance metrics. Key is path (namespace/deployment/pod/cluster) which can be
	// parsed with ParsePath.
	logs       = make(map[string]string)
	stats      = make(map[string]*processlog.Stats)
	importance = make(map[string]int)
	// Aggregated errors for all fetch operations.
	gErrors util.Errors
	lock    = sync.RWMutex{}
)

func runBugReportCommand(_ *cobra.Command) error {
	config, err := parseConfig()
	if err != nil {
		return err
	}

	_, clientset, err := kubeclient.New(config.KubeConfigPath, config.Context)
	if err != nil {
		return fmt.Errorf("could not initialize k8s client: %s ", err)
	}
	resources, err := cluster2.GetClusterResources(context.Background(), clientset)
	if err != nil {
		return err
	}

	log.Infof("Cluster resource tree:\n\n%s\n\n", resources)
	paths, err := filter.GetMatchingPaths(config, resources)
	if err != nil {
		return err
	}

	log.Infof("Fetching logs for the following containers:\n\n%s\n", strings.Join(paths, "\n"))

	gatherInfo(config, resources, paths)
	if len(gErrors) != 0 {
		log.Errora(gErrors.ToError())
	}

	// TODO: sort by importance and discard any over the size limit.
	for path, text := range logs {
		namespace, _, pod, _, err := cluster2.ParsePath(path)
		if err != nil {
			log.Errorf(err.Error())
			continue
		}
		writeFile(archive.ProxyLogPath(tempDir, namespace, pod), text)
	}
	return nil
}

// gatherInfo fetches all logs, resources, debug etc. using goroutines.
// proxy logs and info are saved in logs/stats/importance global maps.
// Errors are reported through gErrors.
func gatherInfo(config *config.BugReportConfig, resources *cluster2.Resources, paths []string) {
	// no timeout on mandatoryWg.
	var mandatoryWg sync.WaitGroup
	cmdTimer := time.NewTimer(time.Duration(config.CommandTimeout))

	clusterDir := archive.ClusterInfoPath(tempDir)
	getFromCluster(content.GetK8sResources, &content.Params{DryRun: config.DryRun}, clusterDir, &mandatoryWg)
	getFromCluster(content.GetCRs, &content.Params{DryRun: config.DryRun}, clusterDir, &mandatoryWg)
	getFromCluster(content.GetEvents, &content.Params{DryRun: config.DryRun}, clusterDir, &mandatoryWg)
	getFromCluster(content.GetClusterInfo, &content.Params{DryRun: config.DryRun}, clusterDir, &mandatoryWg)
	getFromCluster(content.GetSecrets, &content.Params{DryRun: config.DryRun, Verbose: config.FullSecrets}, clusterDir, &mandatoryWg)
	getFromCluster(content.GetDescribePods, &content.Params{DryRun: config.DryRun, Namespace: config.IstioNamespace}, clusterDir, &mandatoryWg)

	// optionalWg is subject to timer.
	var optionalWg sync.WaitGroup
	for _, p := range paths {
		namespace, _, pod, container, err := cluster2.ParsePath(p)
		if err != nil {
			log.Error(err.Error())
			continue
		}

		switch {
		case container == "istio-proxy":
			getFromCluster(content.GetCoredumps, &content.Params{DryRun: config.DryRun, Namespace: namespace, Pod: pod, Container: container},
				archive.ProxyCoredumpPath(tempDir, namespace, pod), &mandatoryWg)
			getProxyLogs(config, resources, p, namespace, pod, container, &optionalWg)

		case strings.HasPrefix(pod, "istiod-") && container == "discovery":
			getFromCluster(content.GetIstiodInfo, &content.Params{DryRun: config.DryRun, Namespace: namespace, Pod: pod, Container: container},
				archive.IstiodPath(tempDir, namespace, pod), &mandatoryWg)
			getIstiodLogs(config, resources, namespace, pod, &mandatoryWg)

		}
	}

	// Not all items are subject to timeout. Proceed only if the non-cancellable items have completed.
	mandatoryWg.Wait()

	// If log fetches have completed, cancel the timeout.
	go func() {
		optionalWg.Wait()
		cmdTimer.Stop()
	}()

	// Wait for log fetches, up to the timeout.
	<-cmdTimer.C
}

// getFromCluster runs a cluster info fetching function f against the cluster and writes the results to fileName.
// Runs if a goroutine, with errors reported through gErrors.
func getFromCluster(f func(params *content.Params) (map[string]string, error), params *content.Params, dir string, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		out, err := f(params)
		appendGlobalErr(err)
		if err == nil {
			writeFiles(dir, out)
		}
	}()
}

// getProxyLogs fetches proxy logs for the given namespace/pod/container and stores the output in global structs.
// Runs if a goroutine, with errors reported through gErrors.
// TODO(stewartbutler): output the logs to a more robust/complete structure.
func getProxyLogs(config *config.BugReportConfig, resources *cluster2.Resources, path, namespace, pod, container string, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		clog, cstat, imp, err := getLog(resources, config, namespace, pod, container)
		appendGlobalErr(err)
		lock.Lock()
		if err == nil {
			logs[path], stats[path], importance[path] = clog, cstat, imp
		}
		lock.Unlock()
	}()
}

// getIstiodLogs fetches Istiod logs for the given namespace/pod and writes the output.
// Runs if a goroutine, with errors reported through gErrors.
func getIstiodLogs(config *config.BugReportConfig, resources *cluster2.Resources, namespace, pod string, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		clog, _, _, err := getLog(resources, config, namespace, pod, "discovery")
		appendGlobalErr(err)
		writeFile(archive.IstiodPath(tempDir, namespace, pod+".log"), clog)
	}()
}

// getLog fetches the logs for the given namespace/pod/container and returns the log text and stats for it.
func getLog(resources *cluster2.Resources, config *config.BugReportConfig, namespace, pod, container string) (string, *processlog.Stats, int, error) {
	log.Infof("Getting logs for %s/%s/%s...", namespace, pod, container)
	previous := resources.ContainerRestarts(pod, container) > 0
	clog, err := kubectlcmd.Logs(namespace, pod, container, previous, config.DryRun)
	if err != nil {
		return "", nil, 0, err
	}
	cstat := &processlog.Stats{}
	clog, cstat, err = processlog.Process(config, clog)
	if err != nil {
		return "", nil, 0, err
	}
	return clog, cstat, cstat.Importance(), nil
}

func writeFiles(dir string, files map[string]string) {
	for fname, text := range files {
		writeFile(filepath.Join(dir, fname), text)
	}
}

func writeFile(path, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	mkdirOrExit(path)
	if err := ioutil.WriteFile(path, []byte(text), 0755); err != nil {
		log.Errorf(err.Error())
	}
}

func mkdirOrExit(fpath string) {
	if err := os.MkdirAll(path.Dir(fpath), 0755); err != nil {
		fmt.Printf("Could not create output directories: %s", err)
		os.Exit(-1)
	}
}

func appendGlobalErr(err error) {
	lock.Lock()
	gErrors = util.AppendErr(gErrors, err)
	lock.Unlock()
}
