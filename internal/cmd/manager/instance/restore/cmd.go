/*
Copyright The CloudNativePG Contributors

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

// Package restore implements the "instance restore" subcommand of the operator
package restore

import (
	"context"
	"errors"
	"os"

	barmanCommand "github.com/cloudnative-pg/barman-cloud/pkg/command"
	"github.com/cloudnative-pg/machinery/pkg/fileutils"
	"github.com/cloudnative-pg/machinery/pkg/log"
	"github.com/spf13/cobra"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cloudnative-pg/cloudnative-pg/internal/management/istio"
	"github.com/cloudnative-pg/cloudnative-pg/internal/management/linkerd"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/management"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/management/postgres"
)

// NewCmd creates the "restore" subcommand
func NewCmd() *cobra.Command {
	var clusterName string
	var namespace string
	var pgData string
	var pgWal string

	cmd := &cobra.Command{
		Use:           "restore [flags]",
		SilenceErrors: true,
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			return management.WaitForGetCluster(cmd.Context(), ctrl.ObjectKey{
				Name:      clusterName,
				Namespace: namespace,
			})
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			info := postgres.InitInfo{
				ClusterName: clusterName,
				Namespace:   namespace,
				PgData:      pgData,
				PgWal:       pgWal,
			}

			return restoreSubCommand(ctx, info)
		},
		PostRunE: func(cmd *cobra.Command, _ []string) error {
			if err := istio.TryInvokeQuitEndpoint(cmd.Context()); err != nil {
				return err
			}

			return linkerd.TryInvokeShutdownEndpoint(cmd.Context())
		},
	}

	cmd.Flags().StringVar(&clusterName, "cluster-name", os.Getenv("CLUSTER_NAME"), "The name of the "+
		"current cluster in k8s, used to coordinate switchover and failover")
	cmd.Flags().StringVar(&namespace, "namespace", os.Getenv("NAMESPACE"), "The namespace of "+
		"the cluster and the Pod in k8s")
	cmd.Flags().StringVar(&pgData, "pg-data", os.Getenv("PGDATA"), "The PGDATA to be restored")
	cmd.Flags().StringVar(&pgWal, "pg-wal", "", "The PGWAL to be restored")

	return cmd
}

func restoreSubCommand(ctx context.Context, info postgres.InitInfo) error {
	contextLogger := log.FromContext(ctx)
	err := info.CheckTargetDataDirectory(ctx)
	if err != nil {
		return err
	}

	err = info.Restore(ctx)
	if err != nil {
		contextLogger.Error(err, "Error while restoring a backup")
		cleanupDataDirectoryIfNeeded(ctx, err, info.PgData)
		return err
	}

	contextLogger.Info("restore command execution completed without errors")

	return nil
}

func cleanupDataDirectoryIfNeeded(ctx context.Context, restoreError error, dataDirectory string) {
	contextLogger := log.FromContext(ctx)

	var barmanError *barmanCommand.CloudRestoreError
	if !errors.As(restoreError, &barmanError) {
		return
	}

	if !barmanError.IsRetriable() {
		return
	}

	contextLogger.Info("Cleaning up data directory", "directory", dataDirectory)
	if err := fileutils.RemoveDirectory(dataDirectory); err != nil && !os.IsNotExist(err) {
		contextLogger.Error(
			err,
			"error occurred cleaning up data directory",
			"directory", dataDirectory)
	}
}
