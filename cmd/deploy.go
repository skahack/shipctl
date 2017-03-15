package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/spf13/cobra"
)

type deployCmd struct {
	cluster     string
	serviceName string
	revision    int
}

func NewDeployCommand(out, errOut io.Writer) *cobra.Command {
	f := &deployCmd{}
	cmd := &cobra.Command{
		Use:   "deploy [options]",
		Short: "",
		RunE: func(cmd *cobra.Command, args []string) error {
			err := f.execute(cmd, args, out)
			if err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&f.cluster, "cluster", "", "ECS Cluster Name")
	cmd.Flags().StringVar(&f.serviceName, "service-name", "", "ECS Service Name")
	cmd.Flags().IntVar(&f.revision, "revision", 0, "revision of ECS task definition")

	return cmd
}

func (f *deployCmd) execute(_ *cobra.Command, args []string, out io.Writer) error {
	if f.cluster == "" {
		return errors.New("--cluster is required")
	}

	if f.serviceName == "" {
		return errors.New("--service-name is required")
	}

	region := func() string {
		if os.Getenv("AWS_REGION") != "" {
			return os.Getenv("AWS_REGION")
		}

		if os.Getenv("AWS_DEFAULT_REGION") != "" {
			return os.Getenv("AWS_DEFAULT_REGION")
		}

		return ""
	}()
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

	service, err := describeService(client, f.cluster, f.serviceName)
	if err != nil {
		return err
	}

	if len(service.Deployments) > 1 {
		return errors.New(fmt.Sprintf("%s is currently deployed", f.serviceName))
	}

	taskDefArn := *service.TaskDefinition
	taskDefArn, err = specifyRevision(f.revision, taskDefArn)
	if err != nil {
		return err
	}

	taskDef, err := describeTaskDefinition(client, taskDefArn)
	if err != nil {
		return err
	}

	newTaskDef, err := registerTaskDefinition(client, taskDef)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "task definition registerd successfully: revision %d -> %d\n", *taskDef.Revision, *newTaskDef.Revision)

	err = updateService(client, service, newTaskDef)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "service updating\n")

	err = waitUpdateService(client, f.cluster, f.serviceName, out)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "service updated successfully\n")

	return nil
}

func describeService(client *ecs.ECS, cluster, serviceName string) (*ecs.Service, error) {
	params := &ecs.DescribeServicesInput{
		Services: []*string{aws.String(serviceName)},
		Cluster:  aws.String(cluster),
	}

	res, err := client.DescribeServices(params)
	if err != nil {
		return nil, err
	}

	if len(res.Services) == 0 {
		return nil, errors.New("service is not found")
	}

	return res.Services[0], nil
}

func describeTaskDefinition(client *ecs.ECS, arn string) (*ecs.TaskDefinition, error) {
	params := &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: aws.String(arn),
	}

	res, err := client.DescribeTaskDefinition(params)
	if err != nil {
		return nil, err
	}

	return res.TaskDefinition, nil
}

func registerTaskDefinition(client *ecs.ECS, taskDef *ecs.TaskDefinition) (*ecs.TaskDefinition, error) {
	params := &ecs.RegisterTaskDefinitionInput{
		ContainerDefinitions: taskDef.ContainerDefinitions,
		Family:               taskDef.Family,
		NetworkMode:          taskDef.NetworkMode,
		PlacementConstraints: taskDef.PlacementConstraints,
		TaskRoleArn:          taskDef.TaskRoleArn,
		Volumes:              taskDef.Volumes,
	}

	res, err := client.RegisterTaskDefinition(params)
	if err != nil {
		return nil, err
	}

	return res.TaskDefinition, nil
}

func updateService(client *ecs.ECS, service *ecs.Service, taskDef *ecs.TaskDefinition) error {
	params := &ecs.UpdateServiceInput{
		Cluster:                 service.ClusterArn,
		DeploymentConfiguration: service.DeploymentConfiguration,
		DesiredCount:            service.DesiredCount,
		Service:                 service.ServiceName,
		TaskDefinition:          taskDef.TaskDefinitionArn,
	}

	_, err := client.UpdateService(params)
	if err != nil {
		return err
	}

	return nil
}

func waitUpdateService(client *ecs.ECS, cluster, serviceName string, out io.Writer) error {
	t := time.NewTicker(10 * time.Second)
	for {
		select {
		case <-t.C:
			s, err := describeService(client, cluster, serviceName)
			if err != nil {
				return err
			}

			for _, v := range s.Deployments {
				fmt.Fprintf(out,
					"status: %s | desired: %d, pending: %d, running: %d\n",
					*v.Status, *v.DesiredCount, *v.PendingCount, *v.RunningCount)
			}

			if len(s.Deployments) == 1 && *s.RunningCount == *s.DesiredCount {
				return nil
			}
		}
	}
}

func specifyRevision(revision int, arn string) (string, error) {
	if revision <= 0 {
		return arn, nil
	}

	re, err := regexp.Compile(`(.*):[1-9][0-9]*$`)
	if err != nil {
		return "", err
	}

	return re.ReplaceAllString(arn, fmt.Sprintf("${1}:%d", revision)), nil
}
