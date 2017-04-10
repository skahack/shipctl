package cmd

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"regexp"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecr"
	"github.com/aws/aws-sdk-go/service/ecs"

	"github.com/docker/distribution/reference"
	"github.com/oklog/ulid"
	"github.com/spf13/cobra"
)

var ECRRegex *regexp.Regexp = func() *regexp.Regexp {
	regex, _ := regexp.Compile(`^[0-9]+\.dkr\.ecr\.(us|ca|eu|ap|sa)-(east|west|central|northeast|southeast|south)-[12]\.amazonaws\.com$`)
	return regex
}()

type deployCmd struct {
	cluster         string
	serviceName     string
	revision        int
	images          imageOptions
	backend         string
	slackWebhookUrl string
}

func NewDeployCommand(out, errOut io.Writer) *cobra.Command {
	f := &deployCmd{}
	cmd := &cobra.Command{
		Use:   "deploy [options]",
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
	cmd.Flags().IntVar(&f.revision, "revision", 0, "revision of ECS task definition")
	cmd.Flags().Var(&f.images, "image", "base image of ECR image")
	cmd.Flags().StringVar(&f.backend, "backend", "SSM", "Backend type of history manager")
	cmd.Flags().StringVar(&f.slackWebhookUrl, "slack-webhook-url", "", "slack webhook URL")

	return cmd
}

func (f *deployCmd) execute(_ *cobra.Command, args []string, l *logger) error {
	if f.cluster == "" {
		return errors.New("--cluster is required")
	}

	if f.serviceName == "" {
		return errors.New("--service-name is required")
	}

	if len(f.images.Value) == 0 {
		return errors.New("--image is required")
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

		for _, v := range taskDef.ContainerDefinitions {
			img, err := parseDockerImage(*v.Image)
			if err != nil {
				return err
			}

			opt := f.images.Get(img.RepositoryName)
			if opt == nil {
				return errors.New(fmt.Sprintf("can not found a image option %s", img.RepositoryName))
			}

			err = tagDockerImage(ecrClient, img.RepositoryName, opt.Tag, uniqueID)
			if err != nil {
				return err
			}
		}

		registerdTaskDef, err = registerTaskDefinition(client, newTaskDef)
		if err != nil {
			return err
		}
	}

	historyManager, err := NewHistoryManager(f.backend, f.cluster, f.serviceName)
	if err != nil {
		return err
	}
	err = historyManager.PushState(
		int(*registerdTaskDef.Revision),
		fmt.Sprintf("deploy: %d -> %d", *taskDef.Revision, *registerdTaskDef.Revision),
	)
	if err != nil {
		return err
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

	err = historyManager.UpdateState(int(*registerdTaskDef.Revision))
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
	newTaskDef := *taskDef // shallow copy
	var containers []*ecs.ContainerDefinition
	for _, vp := range taskDef.ContainerDefinitions {
		v := *vp // shallow copy
		img, err := parseDockerImage(*v.Image)
		if err != nil {
			return nil, err
		}

		if isECRHosted(img) {
			v.Image = aws.String(fmt.Sprintf("%s:%s", img.Name, id))
			containers = append(containers, &v)
		}
	}
	newTaskDef.ContainerDefinitions = containers

	return &newTaskDef, nil
}

type dockerImage struct {
	Name           string
	Tag            string
	RepositoryName string
	HostName       string
}

func parseDockerImage(image string) (*dockerImage, error) {
	ref, err := reference.Parse(image)
	if err != nil {
		return nil, err
	}

	hostName, repoName := reference.SplitHostname(ref.(reference.Named))
	return &dockerImage{
		Name:           ref.(reference.Named).Name(),
		Tag:            ref.(reference.Tagged).Tag(),
		RepositoryName: repoName,
		HostName:       hostName,
	}, nil
}

func isECRHosted(image *dockerImage) bool {
	return ECRRegex.MatchString(image.HostName)
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

func getAWSRegion() string {
	if os.Getenv("AWS_REGION") != "" {
		return os.Getenv("AWS_REGION")
	}

	if os.Getenv("AWS_DEFAULT_REGION") != "" {
		return os.Getenv("AWS_DEFAULT_REGION")
	}

	return ""
}

//
// imageOptions
//

type imageOption struct {
	RepositoryName string
	Tag            string
}

type imageOptions struct {
	Value []*imageOption
}

func (t *imageOptions) String() string {
	return fmt.Sprintf("String: %v", t.Value)
}

func (t *imageOptions) Set(v string) error {
	r, _ := regexp.Compile(`^([a-z0-9]+(?:(?:[._]|__|[-]*)[a-z0-9]+)*):([\w][\w.-]{0,127})$`)
	matches := r.FindStringSubmatch(v)
	if len(matches) == 0 {
		return errors.New(fmt.Sprintf("invalid format %s", v))
	}

	opt := &imageOption{
		RepositoryName: matches[1],
		Tag:            matches[2],
	}

	t.Value = append(t.Value, opt)

	return nil
}

func (t *imageOptions) Type() string {
	return "image"
}

func (t *imageOptions) Get(repoName string) *imageOption {
	for _, v := range t.Value {
		if v.RepositoryName == repoName {
			return v
		}
	}
	return nil
}
