package cmd

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecr"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/docker/distribution/reference"
	"github.com/oklog/ulid"
	"github.com/spf13/cobra"

	slack "github.com/monochromegane/slack-incoming-webhooks"
)

type deployCmd struct {
	cluster     string
	serviceName string
	revision    int
	tag         string
	slackNotify string
}

func NewDeployCommand(out, errOut io.Writer) *cobra.Command {
	f := &deployCmd{}
	cmd := &cobra.Command{
		Use:   "deploy [options]",
		Short: "",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := NewLogger(f.cluster, f.serviceName, f.slackNotify, out)
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
	cmd.Flags().IntVar(&f.revision, "revision", 0, "revision of ECS task definition")
	cmd.Flags().StringVar(&f.tag, "tag", "latest", "base tag of ECR image")
	cmd.Flags().StringVar(&f.slackNotify, "slack-notify", "", "slack webhook URL")

	return cmd
}

func (f *deployCmd) execute(_ *cobra.Command, args []string, l *logger) error {
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

	ecrClient := ecr.New(sess, &aws.Config{
		Region: aws.String(region),
	})

	service, err := describeService(client, f.cluster, f.serviceName)
	if err != nil {
		return err
	}

	if len(service.Deployments) > 1 {
		return errors.New(fmt.Sprintf("%s is currently deployed", f.serviceName))
	}

	var uniqueID string
	{
		entropy := rand.New(rand.NewSource(time.Now().UnixNano()))
		uniqueID = ulid.MustNew(ulid.Now(), entropy).String()
	}

	var taskDef *ecs.TaskDefinition
	var registerdTaskDef *ecs.TaskDefinition
	{
		taskDefArn := *service.TaskDefinition
		taskDefArn, err = specifyRevision(f.revision, taskDefArn)
		if err != nil {
			return err
		}

		taskDef, err = describeTaskDefinition(client, taskDefArn)
		if err != nil {
			return err
		}

		newTaskDef, err := createNewTaskDefinition(uniqueID, taskDef)
		if err != nil {
			return err
		}

		img, err := parseDockerImage(*taskDef.ContainerDefinitions[0].Image)
		if err != nil {
			return err
		}

		err = tagDockerImage(ecrClient, img.RepositoryName, f.tag, uniqueID)
		if err != nil {
			return err
		}

		registerdTaskDef, err = registerTaskDefinition(client, newTaskDef)
		if err != nil {
			return err
		}
	}

	l.log(fmt.Sprintf("task definition registerd successfully: revision %d -> %d\n", *taskDef.Revision, *registerdTaskDef.Revision))

	err = updateService(client, service, registerdTaskDef)
	if err != nil {
		return err
	}

	l.log(fmt.Sprintf("service updating\n"))

	err = waitUpdateService(client, f.cluster, f.serviceName, l)
	if err != nil {
		return err
	}

	l.success(fmt.Sprintf("service updated successfully\n"))

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

func createNewTaskDefinition(id string, taskDef *ecs.TaskDefinition) (*ecs.TaskDefinition, error) {
	if len(taskDef.ContainerDefinitions) > 1 {
		return nil, errors.New("multiple container is not supported")
	}

	newTaskDef := *taskDef // shallow copy
	var containers []*ecs.ContainerDefinition
	for _, vp := range taskDef.ContainerDefinitions {
		v := *vp // shallow copy
		img, err := parseDockerImage(*v.Image)
		if err != nil {
			return nil, err
		}

		v.Image = aws.String(fmt.Sprintf("%s:%s", img.Name, id))
		containers = append(containers, &v)
	}
	newTaskDef.ContainerDefinitions = containers

	return &newTaskDef, nil
}

type dockerImage struct {
	Name           string
	Tag            string
	RepositoryName string
}

func parseDockerImage(image string) (*dockerImage, error) {
	ref, err := reference.Parse(image)
	if err != nil {
		return nil, err
	}
	components := strings.Split(ref.(reference.Named).Name(), "/")
	return &dockerImage{
		Name:           ref.(reference.Named).Name(),
		Tag:            ref.(reference.Tagged).Tag(),
		RepositoryName: components[len(components)-1],
	}, nil
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

func waitUpdateService(client *ecs.ECS, cluster, serviceName string, l *logger) error {
	start := time.Now()
	t := time.NewTicker(10 * time.Second)
	for {
		select {
		case <-t.C:
			s, err := describeService(client, cluster, serviceName)
			if err != nil {
				return err
			}

			elapsed := time.Now().Sub(start)
			l.log(fmt.Sprintf("still service updating... [%s]\n", (elapsed/time.Second)*time.Second))

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

func tagDockerImage(ecrClient *ecr.ECR, repoName string, fromTag string, toTag string) error {
	params := &ecr.BatchGetImageInput{
		ImageIds:       []*ecr.ImageIdentifier{{ImageTag: aws.String(fromTag)}},
		RepositoryName: aws.String(repoName),

		AcceptedMediaTypes: []*string{
			aws.String("application/vnd.docker.distribution.manifest.v1+json"),
			aws.String("application/vnd.docker.distribution.manifest.v2+json"),
			aws.String("application/vnd.oci.image.manifest.v1+json"),
		},
	}
	img, err := ecrClient.BatchGetImage(params)
	if err != nil {
		return err
	}

	putParams := &ecr.PutImageInput{
		ImageManifest:  img.Images[0].ImageManifest,
		RepositoryName: aws.String(repoName),
		ImageTag:       aws.String(toTag),
	}
	_, err = ecrClient.PutImage(putParams)
	if err != nil {
		return err
	}

	return nil
}

type logger struct {
	Cluster         string
	ServiceName     string
	Out             io.Writer
	SlackWebhookUrl string
}

func NewLogger(cluster, serviceName, slackWebhookUrl string, out io.Writer) *logger {
	return &logger{
		Cluster:         cluster,
		ServiceName:     serviceName,
		SlackWebhookUrl: slackWebhookUrl,
		Out:             out,
	}
}

func (l *logger) log(message string) {
	if l.Out != nil {
		fmt.Fprintf(l.Out, message)
	}

	if l.SlackWebhookUrl != "" {
		client := &slack.Client{WebhookURL: l.SlackWebhookUrl}
		payload := &slack.Payload{
			Username: "deploy-bot",
			Text:     fmt.Sprintf("cluster: %s, serviceName: %s\n%s", l.Cluster, l.ServiceName, message),
		}
		client.Post(payload)
	}
}

func (l *logger) logWithType(message string, messageType string) {
	if messageType == "" {
		l.log(message)
		return
	}

	if l.Out != nil {
		fmt.Fprintf(l.Out, message)
	}

	if l.SlackWebhookUrl != "" {
		color := func() string {
			if messageType == "danger" {
				return "danger"
			}
			return "good"
		}()
		client := &slack.Client{WebhookURL: l.SlackWebhookUrl}
		attachment := &slack.Attachment{
			Color: color,
			Text:  fmt.Sprintf("cluster: %s, serviceName: %s\n%s", l.Cluster, l.ServiceName, message),
		}
		payload := &slack.Payload{
			Username:    "deploy-bot",
			Attachments: []*slack.Attachment{attachment},
		}
		client.Post(payload)
	}
}

func (l *logger) success(message string) {
	l.logWithType(message, "good")
}

func (l *logger) fail(message string) {
	l.logWithType(message, "danger")
}
