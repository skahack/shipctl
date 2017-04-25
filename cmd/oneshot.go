package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/spf13/cobra"
)

type oneshotCmd struct {
	cluster     string
	taskDefName string
	command     []string
}

func NewOneshotCommand(out, errOut io.Writer) *cobra.Command {
	f := &oneshotCmd{}
	cmd := &cobra.Command{
		Use:   "oneshot [options] COMMAND",
		Short: "",
		Run: func(cmd *cobra.Command, args []string) {
			f.command = args

			l := NewLogger(f.cluster, f.taskDefName, "", out)
			err := f.execute(cmd, args, l)
			if err != nil {
				l.log(fmt.Sprintf("error: %s\n", err.Error()))
			}
		},
	}
	cmd.Flags().StringVar(&f.cluster, "cluster", "", "ECS cluster name")
	cmd.Flags().StringVar(&f.taskDefName, "taskdef-name", "", "ECS task definition name")

	return cmd
}

func (f *oneshotCmd) execute(_ *cobra.Command, args []string, l *logger) error {
	if f.cluster == "" {
		return errors.New("--cluster is required")
	}

	if f.taskDefName == "" {
		return errors.New("--taskdef-name is required")
	}

	if len(f.command) == 0 {
		return errors.New("COMMAND is required")
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

	taskDef, err := f.describeTaskDefinition(client, f.taskDefName)
	if err != nil {
		return err
	}

	task, err := f.runTask(client, taskDef, f.command)
	if err != nil {
		return err
	}

	l.log("task started\n")

	status, err := f.waitTask(client, task, l)
	if err != nil {
		return err
	}

	os.Exit(status.ExitCode)

	return nil
}

type taskStatus struct {
	ExitCode      int
	StoppedReason string
}

func (f *oneshotCmd) runTask(client *ecs.ECS, taskDef *ecs.TaskDefinition, command []string) (*ecs.Task, error) {
	var commands []*string
	for _, v := range command {
		commands = append(commands, aws.String(v))
	}
	params := &ecs.RunTaskInput{
		Cluster:        aws.String(f.cluster),
		TaskDefinition: taskDef.TaskDefinitionArn,
		Overrides: &ecs.TaskOverride{
			ContainerOverrides: []*ecs.ContainerOverride{
				{
					Name:    aws.String(f.taskDefName),
					Command: commands,
				},
			},
		},
		Count:     aws.Int64(1),
		StartedBy: aws.String("shipctl oneshot"),
	}
	res, err := client.RunTask(params)
	if err != nil {
		return nil, err
	}

	if len(res.Failures) > 0 {
		msg := ""
		for _, v := range res.Failures {
			msg += fmt.Sprintf("    %s\n", *v.Reason)
		}
		return nil, errors.New("failed to runTask\n" + msg)
	}

	return res.Tasks[0], nil
}

func (f *oneshotCmd) waitTask(client *ecs.ECS, task *ecs.Task, l *logger) (*taskStatus, error) {
	start := time.Now()
	t := time.NewTicker(10 * time.Second)
	for {
		select {
		case <-t.C:
			re, err := f.describeTask(client, task)
			if err != nil {
				return nil, err
			}

			elapsed := time.Now().Sub(start)
			l.log(fmt.Sprintf("still running... [%s]\n", (elapsed/time.Second)*time.Second))

			if *re.LastStatus == "STOPPED" {
				status := &taskStatus{
					StoppedReason: *re.StoppedReason,
				}
				if re.Containers[0].ExitCode != nil {
					status.ExitCode = int(*re.Containers[0].ExitCode)
				}
				return status, nil
			}
		}
	}
}

func (f *oneshotCmd) describeTask(client *ecs.ECS, task *ecs.Task) (*ecs.Task, error) {
	params := &ecs.DescribeTasksInput{
		Tasks: []*string{
			task.TaskArn,
		},
		Cluster: task.ClusterArn,
	}
	res, err := client.DescribeTasks(params)
	if err != nil {
		return nil, err
	}

	if len(res.Failures) > 0 {
		msg := ""
		for _, v := range res.Failures {
			msg += fmt.Sprintf("    %s\n", *v.Reason)
		}
		return nil, errors.New("failed to runTask\n" + msg)
	}

	return res.Tasks[0], nil
}

func (f *oneshotCmd) describeTaskDefinition(client *ecs.ECS, name string) (*ecs.TaskDefinition, error) {
	params := &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: aws.String(name),
	}

	res, err := client.DescribeTaskDefinition(params)
	if err != nil {
		return nil, err
	}

	return res.TaskDefinition, nil
}
