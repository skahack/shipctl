package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ssm"
)

type deployStatus int

const (
	deployStatus_UNKNOWN deployStatus = iota
	deployStatus_PENDING
	deployStatus_DEPLOYED
)

type deployState struct {
	Revision int          `json:"revision"`
	Status   deployStatus `json:"status"`
	Cause    string       `json:"cause"`
}

type statePusher interface {
	PushPendingState(int, int) error
	UpdateState(int, int) error
	Pull() ([]*deployState, error)
}

func NewStatePusher(backend, clusterName, serviceName string) (statePusher, error) {
	if backend == "SSM" {
		return NewSSMStatePusher(clusterName, serviceName)
	}
	return NewSSMStatePusher(clusterName, serviceName)
}

type ssmStatePusher struct {
	Client      *ssm.SSM
	ClusterName string
	ServiceName string
}

func NewSSMStatePusher(clusterName, serviceName string) (*ssmStatePusher, error) {
	sess, err := session.NewSession()
	if err != nil {
		return nil, err
	}

	region := getAWSRegion()
	if region == "" {
		return nil, errors.New("AWS region is not found. please set a AWS_DEFAULT_REGION or AWS_REGION")
	}

	client := ssm.New(sess, &aws.Config{
		Region: aws.String(region),
	})

	return &ssmStatePusher{
		Client:      client,
		ClusterName: clusterName,
		ServiceName: serviceName,
	}, nil
}

func (s *ssmStatePusher) Push(v string) error {
	p := &ssm.PutParameterInput{
		Name:      aws.String(s.getName()),
		Type:      aws.String("String"),
		Value:     aws.String(v),
		Overwrite: aws.Bool(true),
	}
	_, err := s.Client.PutParameter(p)
	if err != nil {
		return err
	}
	return nil
}

func (s *ssmStatePusher) PushPendingState(oldRevision, revision int) error {
	state, err := s.Pull()
	if err != nil {
		return err
	}
	for _, v := range state {
		if v.Revision == revision {
			return errors.New(fmt.Sprintf("validation error: revision %d is already exists", revision))
		}
	}
	state = append(state, &deployState{
		Revision: revision,
		Status:   deployStatus_PENDING,
		Cause:    fmt.Sprintf("deploy: %d -> %d", oldRevision, revision),
	})

	from := 0
	if len(state) > 5 {
		from = len(state) - 5
	}

	state = state[from:]
	b, err := json.Marshal(state)
	if err != nil {
		return err
	}

	err = s.Push(string(b))
	if err != nil {
		return err
	}

	return nil
}

func (s *ssmStatePusher) UpdateState(oldRevision, revision int) error {
	state, err := s.Pull()
	if err != nil {
		return err
	}
	for i, v := range state {
		if v.Revision == revision {
			state[i].Status = deployStatus_DEPLOYED

			b, err := json.Marshal(state)
			if err != nil {
				return err
			}

			err = s.Push(string(b))
			if err != nil {
				return err
			}

			return nil
		}
	}

	return errors.New("can not found a current state")
}

func (s *ssmStatePusher) Pull() ([]*deployState, error) {
	filter := &ssm.ParametersFilter{
		Key: aws.String("Name"),
		Values: []*string{
			aws.String(s.getName()),
		},
	}
	filters := []*ssm.ParametersFilter{filter}

	var key *string
	{
		p := &ssm.DescribeParametersInput{
			Filters:    filters,
			MaxResults: aws.Int64(1),
		}
		re, err := s.Client.DescribeParameters(p)
		if err != nil {
			return nil, err
		}
		if len(re.Parameters) == 0 {
			return []*deployState{}, nil
		}

		key = re.Parameters[0].Name
	}

	var v *string
	{
		p := &ssm.GetParametersInput{
			Names:          []*string{key},
			WithDecryption: aws.Bool(false),
		}
		re, err := s.Client.GetParameters(p)
		if err != nil {
			return nil, err
		}

		v = re.Parameters[0].Value
	}

	var states []*deployState
	err := json.NewDecoder(strings.NewReader(*v)).Decode(&states)
	if err != nil {
		return nil, err
	}

	return states, nil
}

func (s *ssmStatePusher) getName() string {
	return fmt.Sprintf("deploy-state.%s.%s", s.ClusterName, s.ServiceName)
}
