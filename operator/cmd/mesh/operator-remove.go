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
	"time"

	"github.com/spf13/cobra"

	"istio.io/istio/operator/pkg/util/clog"
)

type operatorRemoveArgs struct {
	operatorInitArgs
	// force proceeds even if there are validation errors
	force bool
}

type manifestDeleter func(manifestStr, componentName string, opts *Options, l clog.Logger) bool

var (
	defaultManifestDeleter = deleteManifest
)

func addOperatorRemoveFlags(cmd *cobra.Command, oiArgs *operatorRemoveArgs) {
	addOperatorInitFlags(cmd, &oiArgs.operatorInitArgs)
	cmd.PersistentFlags().BoolVar(&oiArgs.force, "force", false, "Proceed even with errors")
}

func operatorRemoveCmd(rootArgs *rootArgs, orArgs *operatorRemoveArgs) *cobra.Command {
	return &cobra.Command{
		Use:   "remove",
		Short: "Removes the Istio operator controller from the cluster.",
		Long:  "The remove subcommand removes the Istio operator controller from the cluster.",
		Args:  cobra.ExactArgs(0),
		Run: func(cmd *cobra.Command, args []string) {
			l := clog.NewConsoleLogger(rootArgs.logToStdErr, cmd.OutOrStdout(), cmd.OutOrStderr())
			operatorRemove(rootArgs, orArgs, l, defaultManifestDeleter)
		}}
}

// operatorRemove removes the Istio operator controller from the cluster.
func operatorRemove(args *rootArgs, orArgs *operatorRemoveArgs, l clog.Logger, deleteManifestFunc manifestDeleter) {
	initLogsOrExit(args)

	installed, err := isControllerInstalled(orArgs.kubeConfigPath, orArgs.context, orArgs.common.operatorNamespace)
	if installed && err != nil {
		l.LogAndFatal(err)
	}
	if !installed {
		l.LogAndPrintf("Operator controller is not installed in %s namespace (no Deployment detected).", orArgs.common.operatorNamespace)
		if !orArgs.force {
			l.LogAndFatal("Aborting, use --force to override.")
		}
	}

	l.LogAndPrintf("Using operator Deployment image: %s/operator:%s", orArgs.common.hub, orArgs.common.tag)

	_, mstr, err := renderOperatorManifest(args, &orArgs.common, l)
	if err != nil {
		l.LogAndFatal(err)
	}

	scope.Debugf("Using the following manifest to remove operator:\n%s\n", mstr)

	opts := &Options{
		DryRun:      args.dryRun,
		WaitTimeout: 1 * time.Minute,
		Kubeconfig:  orArgs.kubeConfigPath,
		Context:     orArgs.context,
	}

	if _, _, err := InitK8SRestClient(opts.Kubeconfig, opts.Context); err != nil {
		l.LogAndFatal(err)
	}

	success := deleteManifestFunc(mstr, "Operator", opts, l)
	if !success {
		l.LogAndPrint("\n*** Errors were logged during manifest deletion. Please check logs above. ***\n")
		return
	}

	l.LogAndPrint("\n*** Success. ***\n")
}

func deleteManifest(_, _ string, _ *Options, l clog.Logger) bool {
	l.LogAndError("Deleting manifest not implemented")
	return false
}
