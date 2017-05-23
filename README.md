# shipctl

shipctl is a cluster operation commands for AWS ECS, simillar to kubectl.

## Installation

Go to the [release page](https://github.com/SKAhack/shipctl/releases).

## Commands

### shipctl deploy

Deploy a specified task definition.

```
$ shipctl deploy [flags]

Flags:
  --backend string             Backend type of history manager (default "SSM")
  --cluster string             ECS Cluster Name
  --image image                base image of ECR image (default String: [])
  --revision int               revision of ECS task definition
  --service-name string        ECS Service Name
  --slack-webhook-url string   slack webhook URL

Example:
  $ shipctl deploy --cluster foo --service-name bar --image "bar:latest"
  $ shipctl deploy --cluster foo --service-name bar --image "bar:latest" --image "baz:latest" --revision 10
```

### shipctl rollback

Rollback previous task definition.

```
$ shipctl rollback [flags]

Flags:
  --backend string             Backend type of state manager (default "SSM")
  --cluster string             ECS Cluster Name
  --service-name string        ECS Service Name
  --slack-webhook-url string   slack webhook URL

Example:
  $ shipctl rollback --cluster foo --service-name bar
```

### shipctl oneshot

Run a specified task on the cluster for one-time job, Inspired by [hako](https://github.com/eagletmt/hako).

```
$ shipctl oneshot [flags] COMMAND

Flags:
  --cluster string        ECS cluster name
  --service-name string   ECS service name. This flag is mutually exclusive of --taskdef-name
  --taskdef-name string   ECS task definition name. This flag is mutually exclusive of --service-name
  --revision int          revision of ECS task definition

Example:
  $ shipctl oneshot --cluster foo --service-name bar echo hello
  $ shipctl oneshot --cluster foo --taskdef-name bar --revision 10 echo hello
```

## License

MIT
