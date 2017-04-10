package cmd

import (
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/spf13/cobra"
)

type rollbackCmd struct {
	cluster         string
	serviceName     string
	backend         string
	slackWebhookUrl string
}

func NewRollbackCommand(out, errOut io.Writer) *cobra.Command {
	f := &rollbackCmd{}
	cmd := &cobra.Command{
		Use:   "rollback [options]",
		Short: "",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := NewLogger(f.cluster, f.serviceName, f.slackWebhookUrl, out)
			err := f.execute(cmd, args, log)
			if err != nil {
				log.fail(fmt.Sprintf("failed to deploy. cluster: %s, serviceName: %s\n", f.cluster, f.serviceName))
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&f.cluster, "cluster", "", "ECS Cluster Name")
	cmd.Flags().StringVar(&f.serviceName, "service-name", "", "ECS Service Name")
	cmd.Flags().StringVar(&f.backend, "backend", "SSM", "Backend type of state manager")
	cmd.Flags().StringVar(&f.slackWebhookUrl, "slack-webhook-url", "", "slack webhook URL")

	return cmd
}

func (f *rollbackCmd) execute(_ *cobra.Command, args []string, l *logger) error {
	if f.cluster == "" {
		return errors.New("--cluster is required")
	}

	if f.serviceName == "" {
		return errors.New("--service-name is required")
	}

	region := getAWSRegion()
	if region == "" {
		return errors.New("AWS region is not found. please set a AWS_DEFAULT_REGION or AWS_REGION")
	}

	sess, err := session.NewSession()
	if err != nil {
		return err
	}

	client := ecs.New(sess, &aws.Config{
		Region: aws.String(region),
	})

	historyManager, err := NewHistoryManager(f.backend, f.cluster, f.serviceName)
	if err != nil {
		return err
	}

	states, err := historyManager.Pull()
	if err != nil {
		return err
	}
	if len(states) < 2 {
		return errors.New("can not found a prev state")
	}

	prevState := states[len(states)-2]
	state := states[len(states)-1]

	service, err := describeService(client, f.cluster, f.serviceName)
	if err != nil {
		return err
	}

	if len(service.Deployments) > 1 {
		return errors.New(fmt.Sprintf("%s is currently deploying", f.serviceName))
	}

	var taskDef *ecs.TaskDefinition
	{
		taskDefArn := *service.TaskDefinition
		taskDefArn, err = specifyRevision(prevState.Revision, taskDefArn)
		if err != nil {
			return err
		}

		taskDef, err = describeTaskDefinition(client, taskDefArn)
		if err != nil {
			return err
		}
	}

	l.log(fmt.Sprintf("rollback: revision %d -> %d\n", state.Revision, prevState.Revision))

	err = updateService(client, service, taskDef)
	if err != nil {
		return err
	}

	l.log(fmt.Sprintf("service updating\n"))

	err = waitUpdateService(client, f.cluster, f.serviceName, l)
	if err != nil {
		return err
	}

	err = historyManager.PushState(
		prevState.Revision,
		fmt.Sprintf("rollback: %d -> %d", state.Revision, prevState.Revision),
	)
	if err != nil {
		return err
	}

	l.success(fmt.Sprintf("service updated successfully\n"))

	return nil
}
