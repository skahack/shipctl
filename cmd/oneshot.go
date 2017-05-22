package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/spf13/cobra"

	libecs "github.com/SKAhack/shipctl/lib/ecs"
	log "github.com/SKAhack/shipctl/lib/logger"
)

type oneshotCmd struct {
	cluster     string
	taskDefName string
	serviceName string
	command     []string
	revision    int
	shellExec   bool
}

func NewOneshotCommand(out, errOut io.Writer) *cobra.Command {
	f := &oneshotCmd{}
	cmd := &cobra.Command{
		Use:   "oneshot [options] COMMAND",
		Short: "",
		Run: func(cmd *cobra.Command, args []string) {
			f.command = args

			l := log.NewLogger(f.cluster, f.taskDefName, "", out)
			err := f.execute(cmd, args, l)
			if err != nil {
				l.Log(fmt.Sprintf("error: %s\n", err.Error()))
			}
		},
	}
	cmd.Flags().StringVar(&f.cluster, "cluster", "", "ECS cluster name")
	cmd.Flags().StringVar(&f.taskDefName, "taskdef-name", "", "ECS task definition name")
	cmd.Flags().IntVar(&f.revision, "revision", 0, "revision of ECS task definition")
	cmd.Flags().StringVar(&f.serviceName, "service-name", "", "ECS service name")

	return cmd
}

type specifyingTaskDefStrategy int

const (
	TASK_DEFINITION specifyingTaskDefStrategy = iota
	SERVICE
)

func (f *oneshotCmd) execute(_ *cobra.Command, args []string, l *log.Logger) error {
	strategy := TASK_DEFINITION

	if f.cluster == "" {
		return errors.New("--cluster is required")
	}

	if f.taskDefName == "" && f.serviceName == "" {
		return errors.New("--taskdef-name or --service-name is required")
	}

	if f.taskDefName != "" {
		strategy = TASK_DEFINITION
	} else {
		strategy = SERVICE
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

	var arn string
	if strategy == TASK_DEFINITION {
		taskDef, err := libecs.DescribeTaskDefinition(client, f.taskDefName)
		if err != nil {
			return err
		}
		arn = *taskDef.TaskDefinitionArn
	} else {
		service, err := libecs.DescribeService(client, f.cluster, f.serviceName)
		if err != nil {
			return err
		}
		arn = *service.TaskDefinition
	}

	arn, err = libecs.SpecifyRevision(f.revision, arn)
	if err != nil {
		return err
	}

	taskDef, err := libecs.DescribeTaskDefinition(client, arn)
	if err != nil {
		return err
	}

	task, err := f.runTask(client, taskDef, f.command)
	if err != nil {
		return err
	}

	l.Log("task started\n")

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
					Name:    taskDef.ContainerDefinitions[0].Name,
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

func (f *oneshotCmd) waitTask(client *ecs.ECS, task *ecs.Task, l *log.Logger) (*taskStatus, error) {
	start := time.Now()
	sig := make(chan os.Signal)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	t := time.NewTicker(10 * time.Second)
	label := "running"
	for {
		select {
		case <-t.C:
			re, err := f.describeTask(client, task)
			if err != nil {
				return nil, err
			}

			elapsed := time.Now().Sub(start)
			l.Log(fmt.Sprintf("still %s... [%s]\n", label, (elapsed/time.Second)*time.Second))

			if *re.LastStatus == "STOPPED" {
				status := &taskStatus{
					StoppedReason: *re.StoppedReason,
				}
				if re.Containers[0].ExitCode != nil {
					status.ExitCode = int(*re.Containers[0].ExitCode)
				}
				return status, nil
			}
		case <-sig:
			f.stopTask(client, task)
			l.Log(fmt.Sprintf("send stop signal\n"))
			label = "stopping"
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

func (f *oneshotCmd) stopTask(client *ecs.ECS, task *ecs.Task) error {
	params := &ecs.StopTaskInput{
		Cluster: task.ClusterArn,
		Reason:  aws.String("SIGINT"),
		Task:    task.TaskArn,
	}

	_, err := client.StopTask(params)
	if err != nil {
		return err
	}

	return nil
}
