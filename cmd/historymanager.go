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

const defaultHistoryLimit int = 5

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

type historyManager interface {
	PushState(int, string) error
	UpdateState(int) error
	Pull() ([]*deployState, error)
}

func NewHistoryManager(backend, clusterName, serviceName string) (historyManager, error) {
	if backend == "SSM" {
		return NewSSMHistoryManager(clusterName, serviceName)
	}
	return NewSSMHistoryManager(clusterName, serviceName)
}

type ssmHistoryManager struct {
	Client       *ssm.SSM
	ClusterName  string
	ServiceName  string
	HistoryLimit int
}

func NewSSMHistoryManager(clusterName, serviceName string) (*ssmHistoryManager, error) {
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

	return &ssmHistoryManager{
		Client:       client,
		ClusterName:  clusterName,
		ServiceName:  serviceName,
		HistoryLimit: defaultHistoryLimit,
	}, nil
}

func (s *ssmHistoryManager) Push(v string) error {
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

func (s *ssmHistoryManager) PushState(revision int, cause string) error {
	state, err := s.Pull()
	if err != nil {
		return err
	}
	state = append(state, &deployState{
		Revision: revision,
		Status:   deployStatus_PENDING,
		Cause:    cause,
	})

	from := 0
	if len(state) > s.HistoryLimit {
		from = len(state) - s.HistoryLimit
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

func (s *ssmHistoryManager) UpdateState(revision int) error {
	state, err := s.Pull()
	if err != nil {
		return err
	}
	for i, v := range state {
		if v.Revision == revision && v.Status == deployStatus_PENDING {
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

func (s *ssmHistoryManager) Pull() ([]*deployState, error) {
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

func (s *ssmHistoryManager) getName() string {
	return fmt.Sprintf("deploy-state.%s.%s", s.ClusterName, s.ServiceName)
}
