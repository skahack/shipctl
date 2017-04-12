package cmd

import (
	"fmt"
	"io"

	slack "github.com/monochromegane/slack-incoming-webhooks"
)

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
}

func (l *logger) slack(messageType string, message string) {
	if l.SlackWebhookUrl == "" {
		return
	}

	switch messageType {
	case "normal":
		client := &slack.Client{WebhookURL: l.SlackWebhookUrl}
		payload := &slack.Payload{
			Username: "deploy-bot",
			Text:     fmt.Sprintf("cluster: %s, serviceName: %s\n%s", l.Cluster, l.ServiceName, message),
		}
		client.Post(payload)
	case "good":
	case "danger":
		client := &slack.Client{WebhookURL: l.SlackWebhookUrl}
		attachment := &slack.Attachment{
			Color: messageType,
			Text:  fmt.Sprintf("cluster: %s, serviceName: %s\n%s", l.Cluster, l.ServiceName, message),
		}
		payload := &slack.Payload{
			Username:    "deploy-bot",
			Attachments: []*slack.Attachment{attachment},
		}
		client.Post(payload)
	}
}
