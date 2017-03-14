package cmd

import (
	"errors"

	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/spf13/cobra"
)

type deployCmd struct {
	cluster     string
	serviceName string
	rev         int
}

func NewDeployCommand() *cobra.Command {
	f := &deployCmd{}
	cmd := &cobra.Command{
		Use:   "deploy [options]",
		Short: "",
		RunE:  f.execute,
	}
	cmd.Flags().StringVar(&f.cluster, "cluster", "", "ECS Cluster Name")
	cmd.Flags().StringVar(&f.serviceName, "service-name", "", "ECS Service Name")
	cmd.Flags().IntVar(&f.rev, "rev", 0, "revision of ECS task definition")

	return cmd
}

func (f *deployCmd) execute(_ *cobra.Command, args []string) error {
	if f.cluster == "" {
		return errors.New("--cluster is required")
	}

	if f.serviceName == "" {
		return errors.New("--service-name is required")
	}

	_, err := session.NewSession()
	if err != nil {
		return errors.New("failed to establish AWS session")
	}

	return nil
}
