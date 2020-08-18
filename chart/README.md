# Cronjobber Helm Chart

## Chart Details
This chart will do the following:

* Installing `Cronjobber` which is the cronjob controller from Kubernetes patched with time zone support.

## Installing the Chart

To install the chart with the release name `my-release`:

```bash
$ helm install my-release chart/
```

## Configuration

The following table lists the configurable parameters of the cronjobber chart and their default values.

| Parameter               | Description                           | Default                                                    |
| ----------------------- | ----------------------------------    | ---------------------------------------------------------- |
| `name`                  | Name of the resources                 | `cronjobber`                                               |
| `namespace`             | Namespace to deploy the resources     | `default`                                                  |
| `image.repository`      | Container image name                  | `quay.io/hiddeco/cronjobber`                               |
| `image.tag`             | Container image tag                   | `0.2.0`                                                    |
| `replicas`              | Number of replicas                    | `1`                                                        |
| `resources.requests.cpu`| CPU request for the main container    | `50m`                                                      |
| `resources.requests.memory`| Memory request for the main container | `64Mi`                                                  |
| `sidecar.enabled`       | Sidecar to keep the timezone database up-to-date | `False`                                         |
| `sidecar.name`          | Init container name                   | `init-updatetz`                                            |
| `sidecar.image.repository`| Sidecar and init container image name | `quay.io/hiddeco/cronjobber-updatetz`                    |
| `sidecar.image.tag`     | Sidecar and init container image tag  | `0.1.1`                                                    |
| `sidecar.resources.requests.cpu`| Sidecar and init container cpu request | `100m`                                            |
| `sidecar.resources.requests.memory`| Sidecar and init container memory request | `64Mi`                                      |


Specify each parameter using the `--set key=value[,key=value]` argument to `helm install`.

Alternatively, a YAML file that specifies the values for the parameters can be provided while installing the chart. For example,

```bash
$ helm install --name my-release -f values.yaml chart
```

> **Tip**: You can use the default [values.yaml](values.yaml)

## Usage

Instead of creating a [`CronJob`](https://kubernetes.io/docs/tasks/job/automated-tasks-with-cron-jobs/)
like you normally would, you create a `TZCronJob`, which works exactly
the same but supports an additional field: `.spec.timezone`. Set this
to the time zone you wish to schedule your jobs in and Cronjobber will
take care of the rest.

```yaml
apiVersion: cronjobber.hidde.co/v1alpha1
kind: TZCronJob
metadata:
  name: hello
spec:
  schedule: "*/1 * * * *"
  timezone: "Europe/Amsterdam"
  jobTemplate:
    spec:
      template:
        spec:
          containers:
          - name: hello
            image: busybox
            args:
            - /bin/sh
            - -c
            - date; echo "Hello, World!"
          restartPolicy: OnFailure
```
