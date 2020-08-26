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
	"encoding/json"
	"fmt"
	"io/ioutil"
	"reflect"
	"time"

	jsonpatch "github.com/evanphx/json-patch"
	"github.com/ghodss/yaml"
	"github.com/spf13/cobra"

	config2 "istio.io/istio/tools/bug-report/pkg/config"
	"istio.io/pkg/log"
)

var (
	startTime, endTime, configFile, tempDir string
	included, excluded                      []string
	commandTimeout, since                   time.Duration
	gConfig                                 = &config2.BugReportConfig{}
)

func addFlags(cmd *cobra.Command, args *config2.BugReportConfig) {
	// k8s client config
	cmd.PersistentFlags().StringVarP(&args.KubeConfigPath, "kubeconfig", "c", "", bugReportHelpKubeconfig)
	cmd.PersistentFlags().StringVar(&args.Context, "context", "", bugReportHelpContext)

	// input config
	cmd.PersistentFlags().StringVarP(&configFile, "filename", "f", "", bugReportHelpFilename)

	// dry run
	cmd.PersistentFlags().BoolVarP(&args.DryRun, "dry-run", "", false, bugReportHelpDryRun)

	// istio namespaces
	cmd.PersistentFlags().StringSliceVarP(&args.IstioNamespaces, "namespaces", "n", bugReportDefaultIstioNamespaces, bugReportHelpIstioNamespaces)

	// timeouts and max sizes
	cmd.PersistentFlags().DurationVar(&commandTimeout, "timeout", bugReportDefaultTimeout, bugReportHelpCommandTimeout)
	cmd.PersistentFlags().Int32Var(&args.MaxArchiveSizeMb, "max-size", bugReportDefaultMaxSizeMb, bugReportHelpMaxArchiveSizeMb)

	// include / exclude specs
	cmd.PersistentFlags().StringSliceVarP(&included, "include", "i", bugReportDefaultInclude, bugReportHelpInclude)
	cmd.PersistentFlags().StringSliceVarP(&excluded, "exclude", "e", bugReportDefaultExclude, bugReportHelpExclude)

	// log time ranges
	cmd.PersistentFlags().StringVar(&startTime, "start-time", "", bugReportHelpStartTime)
	cmd.PersistentFlags().StringVar(&endTime, "end-time", "", bugReportHelpEndTime)
	cmd.PersistentFlags().DurationVar(&since, "duration", 0, bugReportHelpSince)

	// log error control
	cmd.PersistentFlags().StringSliceVar(&args.CriticalErrors, "critical-errs", nil, bugReportHelpCriticalErrors)
	cmd.PersistentFlags().StringSliceVar(&args.WhitelistedErrors, "whitelist-errs", nil, bugReportHelpWhitelistedErrors)

	// archive and upload control
	cmd.PersistentFlags().StringVar(&args.GCSURL, "gcs-url", "", bugReportHelpGCSURL)
	cmd.PersistentFlags().BoolVar(&args.UploadToGCS, "upload", false, bugReportHelpUploadToGCS)

	// output/working dir
	cmd.PersistentFlags().StringVar(&args.TempDir, "working-dir", bugReportDefaultTempDir, bugReportHelpTempDir)
	cmd.PersistentFlags().StringVar(&args.OutputDir, "output-dir", "./", bugReportHelpOutputDir)
}

func parseConfig() (*config2.BugReportConfig, error) {
	if err := parseTimes(gConfig, startTime, endTime); err != nil {
		log.Fatal(err.Error())
	}
	for _, s := range included {
		ss := &config2.SelectionSpec{}
		if err := ss.UnmarshalJSON([]byte(s)); err != nil {
			return nil, err
		}
		gConfig.Include = append(gConfig.Include, ss)
	}
	for _, s := range excluded {
		ss := &config2.SelectionSpec{}
		if err := ss.UnmarshalJSON([]byte(s)); err != nil {
			return nil, err
		}
		gConfig.Exclude = append(gConfig.Exclude, ss)
	}

	fileConfig := &config2.BugReportConfig{}
	if configFile != "" {
		b, err := ioutil.ReadFile(configFile)
		if err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal(b, fileConfig); err != nil {
			return nil, err
		}
		gConfig, err = overlayConfig(gConfig, fileConfig)
		if err != nil {
			return nil, err
		}
	}

	return gConfig, nil
}

func parseTimes(config *config2.BugReportConfig, startTime, endTime string) error {
	config.EndTime = time.Now()
	if endTime != "" {
		var err error
		config.EndTime, err = time.Parse(time.RFC3339, endTime)
		if err != nil {
			return fmt.Errorf("bad format for end-time: %s, expect RFC3339 e.g. %s", endTime, time.RFC3339)
		}
	}
	if config.Since != 0 {
		if startTime != "" {
			return fmt.Errorf("only one --start-time or --Since may be set")
		}
		config.StartTime = config.EndTime.Add(-1 * time.Duration(config.Since))
	} else {
		var err error
		if startTime == "" {
			config.StartTime = time.Time{}
		} else {
			config.StartTime, err = time.Parse(time.RFC3339, startTime)
			if err != nil {
				return fmt.Errorf("bad format for start-time: %s, expect RFC3339 e.g. %s", startTime, time.RFC3339)
			}
		}
	}
	return nil
}

func overlayConfig(base, overlay *config2.BugReportConfig) (*config2.BugReportConfig, error) {
	bj, err := json.Marshal(base)
	if err != nil {
		return nil, err
	}
	oj, err := json.Marshal(base)
	if err != nil {
		return nil, err
	}

	mj, err := jsonpatch.MergePatch(bj, oj)
	if err != nil {
		return nil, fmt.Errorf("json merge error (%s) for base object: \n%s\n override object: \n%s", err, bj, oj)
	}

	out := &config2.BugReportConfig{}
	err = json.Unmarshal(mj, out)
	return out, err
}
