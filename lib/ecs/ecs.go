package ecs

import (
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"

	log "github.com/SKAhack/shipctl/lib/logger"
)

func DescribeService(client *ecs.ECS, cluster, serviceName string) (*ecs.Service, error) {
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

func DescribeTaskDefinition(client *ecs.ECS, arn string) (*ecs.TaskDefinition, error) {
	params := &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: aws.String(arn),
	}

	res, err := client.DescribeTaskDefinition(params)
	if err != nil {
		return nil, err
	}

	return res.TaskDefinition, nil
}

func SpecifyRevision(revision int, arn string) (string, error) {
	if revision <= 0 {
		return arn, nil
	}

	re, err := regexp.Compile(`(.*):[1-9][0-9]*$`)
	if err != nil {
		return "", err
	}

	return re.ReplaceAllString(arn, fmt.Sprintf("${1}:%d", revision)), nil
}

func UpdateService(client *ecs.ECS, service *ecs.Service, taskDef *ecs.TaskDefinition) error {
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

func WaitUpdateService(client *ecs.ECS, cluster, serviceName string, l *log.Logger) error {
	start := time.Now()
	t := time.NewTicker(10 * time.Second)
	for {
		select {
		case <-t.C:
			s, err := DescribeService(client, cluster, serviceName)
			if err != nil {
				return err
			}

			elapsed := time.Now().Sub(start)
			l.Log(fmt.Sprintf("still service updating... [%s]\n", (elapsed/time.Second)*time.Second))

			if len(s.Deployments) == 1 && *s.RunningCount == *s.DesiredCount {
				return nil
			}
		}
	}
}
