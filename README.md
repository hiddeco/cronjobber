# Cronjobber

[![CircleCI](https://circleci.com/gh/hiddeco/cronjobber/tree/master.svg?style=shield)](https://circleci.com/gh/hiddeco/cronjobber/tree/master)
[![Go Report Card](https://goreportcard.com/badge/github.com/hiddeco/cronjobber)](https://goreportcard.com/report/github.com/hiddeco/cronjobber)
[![Docker Repository on Quay](https://quay.io/repository/hiddeco/cronjobber/status "Docker Repository on Quay")](https://quay.io/repository/hiddeco/cronjobber)
[![License](https://img.shields.io/github/license/hiddeco/cronjobber.svg)](https://github.com/hiddeco/cronjobber/blob/master/LICENSE)
[![GitHub release](https://img.shields.io/github/release/hiddeco/cronjobber.svg)](https://github.com/hiddeco/cronjobber/releases)

Cronjobber is the cronjob controller from Kubernetes patched with time zone support.

## Installation

```sh
# Install Controller
https://raw.githubusercontent.com/kkothule/cronjobber/master/deploy/manifests.yaml

```
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

## Reasoning

There has been a [long outstanding (and now closed) issue](https://github.com/kubernetes/kubernetes/issues/47202)
to add time zone support to the `CronJob` kind in Kubernetes, including
a [fully working PR](https://github.com/kubernetes/kubernetes/pull/47266)
which actually made it possible. SIG Apps and in SIG Architecture
decided however against adding it because of the downside of having
to manage and distribute time zone databases.

> People are now encouraged to innovate and solve these kinds of problems in the ecosystem rather than core.
>
> Instead of putting this in Kubernetes the ask is to:
> 1. Develop this in the ecosystem (e.g., a controller) that others can use. Distribute it, solve the problems there, and see what update looks like
> 2. If the solution is widely adopted and can be used by everyone (including small scale, multi-cluster, etc) then it could be considered for core Kubernetes
>
> -- <cite>[mattfarina (Matt Farina) on Jan 26, 2018](https://github.com/kubernetes/kubernetes/issues/47202#issuecomment-360820586)</cite>

Cronjobber is the most simple answer to this: it is the original PR
on top of a more recent version of the cronjob controller, with some
glue added to make it an independent controller.

## Credits

This application is derived from open source components. You can find
the original source code of these components below.

* [Kubernetes CronJob controller](https://github.com/kubernetes/kubernetes/tree/v1.13.3/pkg/controller/cronjob)
* [kubernetes/kubernetes#47266](https://github.com/kubernetes/kubernetes/pull/47266) by Adam Sunderland
* https://book.kubebuilder.io/quick-start.html