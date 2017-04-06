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
