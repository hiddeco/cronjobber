/*
Copyright 2016 The Kubernetes Authors.
Copyright 2019 The Cronjobber Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cronjobber

/*
I did not use watch or expectations.  Those add a lot of corner cases, and we aren't
expecting a large volume of jobs or scheduledJobs.  (We are favoring correctness
over scalability.  If we find a single controller thread is too slow because
there are a lot of Jobs or CronJobs, we can parallelize by Namespace.
If we find the load on the API server is too high, we can use a watch and
UndeltaStore.)

Just periodically list jobs and SJs, and then reconcile them.

*/

import (
	"fmt"
	"sort"
	"time"

	"go.uber.org/zap"
	"k8s.io/klog"

	cronjobberv1 "github.com/hiddeco/cronjobber/pkg/apis/cronjobber/v1alpha1"
	cronjobberclientset "github.com/hiddeco/cronjobber/pkg/client/clientset/versioned"
	cronjobberscheme "github.com/hiddeco/cronjobber/pkg/client/clientset/versioned/scheme"
	cronjobberinformers "github.com/hiddeco/cronjobber/pkg/client/informers/externalversions/cronjobber/v1alpha1"
	cronjobberlisters "github.com/hiddeco/cronjobber/pkg/client/listers/cronjobber/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	ref "k8s.io/client-go/tools/reference"
	"k8s.io/kubernetes/pkg/util/metrics"
)

// Utilities for dealing with Jobs and TZCronJobs and time and timezones.

// controllerKind contains the schema.GroupVersionKind for this controller type.
var controllerKind = batchv1beta1.SchemeGroupVersion.WithKind("TZCronJob")

type TZCronJobController struct {
	kubeClient      clientset.Interface
	tzCronJobLister cronjobberlisters.TZCronJobLister
	jobControl      jobControlInterface
	sjControl       sjControlInterface
	podControl      podControlInterface
	recorder        record.EventRecorder
	logger          *zap.SugaredLogger
}

func NewTZCronJobController(kubeClient clientset.Interface, cronjobberClient cronjobberclientset.Interface,
	cronjobberInformer cronjobberinformers.TZCronJobInformer, logger *zap.SugaredLogger) (*TZCronJobController, error) {
	cronjobberscheme.AddToScheme(scheme.Scheme)
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(logger.Named("event-broadcaster").Debugf)
	eventBroadcaster.StartRecordingToSink(&v1core.EventSinkImpl{Interface: kubeClient.CoreV1().Events("")})

	if kubeClient != nil && kubeClient.CoreV1().RESTClient().GetRateLimiter() != nil {
		if err := metrics.RegisterMetricAndTrackRateLimiterUsage("cronjobber_controller", kubeClient.CoreV1().RESTClient().GetRateLimiter()); err != nil {
			return nil, err
		}
	}

	jm := &TZCronJobController{
		kubeClient:      kubeClient,
		tzCronJobLister: cronjobberInformer.Lister(),
		jobControl:      realJobControl{KubeClient: kubeClient},
		sjControl:       &realSJControl{CronJobberClient: cronjobberClient},
		podControl:      &realPodControl{KubeClient: kubeClient},
		recorder:        eventBroadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: "cronjobber"}),
		logger:          logger,
	}

	return jm, nil
}

// Run the main goroutine responsible for watching and syncing jobs.
func (jm *TZCronJobController) Run(stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	jm.logger.Infof("Starting TZCronJob Manager")
	// Check things every 10 second.
	go wait.Until(jm.syncAll, 10*time.Second, stopCh)
	<-stopCh
	jm.logger.Infof("Shutting down TZCronJob Manager")
}

// syncAll lists all the CronJobs and Jobs and reconciles them.
func (jm *TZCronJobController) syncAll() {
	// List children (Jobs) before parents (TZCronJob).
	// This guarantees that if we see any Job that got orphaned by the GC orphan finalizer,
	// we must also see that the parent TZCronJob has non-nil DeletionTimestamp
	// (see kubernetes/kubernetes#42639).
	// Note that this only works because we are NOT using any caches here.
	jl, err := jm.kubeClient.BatchV1().Jobs(metav1.NamespaceAll).List(metav1.ListOptions{})
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("can't list Jobs: %v", err))
		return
	}
	js := jl.Items
	jm.logger.Debugf("Found %d jobs", len(js))

	sjs, err := jm.tzCronJobLister.TZCronJobs(metav1.NamespaceAll).List(labels.Everything())
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("can't list TZCronJobs: %v", err))
		return
	}
	jm.logger.Debugf("Found %d cronjobs", len(sjs))

	jobsBySj := groupJobsByParent(js, jm.logger)
	jm.logger.Debugf("Found %d groups", len(jobsBySj))

	for _, sj := range sjs {
		tzTime, err := getCurrentTimeInZone(sj)
		if err != nil {
			// We validate that the TZCronJob time zone is valid.
			// However, it is possible that the list of time zones could change at runtime which would cause an error when getting the time.
			jm.recorder.Eventf(sj, v1.EventTypeWarning, "InvalidTimeZone", "Attempted to run a job with an invalid time zone: %v", sj.Spec.TimeZone)
		}

		syncOne(sj, jobsBySj[sj.UID], tzTime, jm.jobControl, jm.sjControl, jm.recorder, jm.logger)
		cleanupFinishedJobs(sj, jobsBySj[sj.UID], jm.jobControl, jm.sjControl, jm.recorder, jm.logger)
	}
}

// cleanupFinishedJobs cleanups finished jobs created by a TZCronJob
func cleanupFinishedJobs(sj *cronjobberv1.TZCronJob, js []batchv1.Job, jc jobControlInterface,
	sjc sjControlInterface, recorder record.EventRecorder, logger *zap.SugaredLogger) {
	// If neither limits are active, there is no need to do anything.
	if sj.Spec.FailedJobsHistoryLimit == nil && sj.Spec.SuccessfulJobsHistoryLimit == nil {
		return
	}

	failedJobs := []batchv1.Job{}
	succesfulJobs := []batchv1.Job{}

	for _, job := range js {
		isFinished, finishedStatus := getFinishedStatus(&job)
		if isFinished && finishedStatus == batchv1.JobComplete {
			succesfulJobs = append(succesfulJobs, job)
		} else if isFinished && finishedStatus == batchv1.JobFailed {
			failedJobs = append(failedJobs, job)
		}
	}

	if sj.Spec.SuccessfulJobsHistoryLimit != nil {
		removeOldestJobs(sj,
			succesfulJobs,
			jc,
			*sj.Spec.SuccessfulJobsHistoryLimit,
			recorder,
			logger)
	}

	if sj.Spec.FailedJobsHistoryLimit != nil {
		removeOldestJobs(sj,
			failedJobs,
			jc,
			*sj.Spec.FailedJobsHistoryLimit,
			recorder,
			logger)
	}

	// Update the TZCronJob, in case jobs were removed from the list.
	if _, err := sjc.UpdateStatus(sj); err != nil {
		nameForLog := fmt.Sprintf("%s/%s", sj.Namespace, sj.Name)
		logger.Infof("Unable to update status for %s (rv = %s): %v", nameForLog, sj.ResourceVersion, err)
	}
}

// removeOldestJobs removes the oldest jobs from a list of jobs
func removeOldestJobs(sj *cronjobberv1.TZCronJob, js []batchv1.Job, jc jobControlInterface,
	maxJobs int32, recorder record.EventRecorder, logger *zap.SugaredLogger) {
	numToDelete := len(js) - int(maxJobs)
	if numToDelete <= 0 {
		return
	}

	nameForLog := fmt.Sprintf("%s/%s", sj.Namespace, sj.Name)
	logger.Infof("Cleaning up %d/%d jobs from %s", numToDelete, len(js), nameForLog)

	sort.Sort(byJobStartTime(js))
	for i := 0; i < numToDelete; i++ {
		logger.Debugf("Removing job %s from %s", js[i].Name, nameForLog)
		deleteJob(sj, &js[i], jc, recorder, logger)
	}
}

// syncOne reconciles a TZCronJob with a list of any Jobs that it created.
// All known jobs created by "sj" should be included in "js".
// The current time is passed in to facilitate testing.
// It has no receiver, to facilitate testing.
func syncOne(sj *cronjobberv1.TZCronJob, js []batchv1.Job, now time.Time, jc jobControlInterface,
	sjc sjControlInterface, recorder record.EventRecorder, logger *zap.SugaredLogger) {
	nameForLog := fmt.Sprintf("%s/%s", sj.Namespace, sj.Name)

	childrenJobs := make(map[types.UID]bool)
	for _, j := range js {
		childrenJobs[j.ObjectMeta.UID] = true
		found := inActiveList(*sj, j.ObjectMeta.UID)
		if !found && !IsJobFinished(&j) {
			recorder.Eventf(sj, v1.EventTypeWarning, "UnexpectedJob", "Saw a job that the controller did not create or forgot: %v", j.Name)
			// We found an unfinished job that has us as the parent, but it is not in our Active list.
			// This could happen if we crashed right after creating the Job and before updating the status,
			// or if our jobs list is newer than our sj status after a relist, or if someone intentionally created
			// a job that they wanted us to adopt.

			// TODO: maybe handle the adoption case?  Concurrency/suspend rules will not apply in that case, obviously, since we can't
			// stop users from creating jobs if they have permission.  It is assumed that if a
			// user has permission to create a job within a namespace, then they have permission to make any scheduledJob
			// in the same namespace "adopt" that job.  ReplicaSets and their Pods work the same way.
			// TBS: how to update sj.Status.LastScheduleTime if the adopted job is newer than any we knew about?
		} else if found && IsJobFinished(&j) {
			deleteFromActiveList(sj, j.ObjectMeta.UID)
			// TODO: event to call out failure vs success.
			recorder.Eventf(sj, v1.EventTypeNormal, "SawCompletedJob", "Saw completed job: %v", j.Name)
		}
	}

	// Remove any job reference from the active list if the corresponding job does not exist any more.
	// Otherwise, the cronjob may be stuck in active mode forever even though there is no matching
	// job running.
	for _, j := range sj.Status.Active {
		if found := childrenJobs[j.UID]; !found {
			recorder.Eventf(sj, v1.EventTypeNormal, "MissingJob", "Active job went missing: %v", j.Name)
			deleteFromActiveList(sj, j.UID)
		}
	}

	updatedSJ, err := sjc.UpdateStatus(sj)
	if err != nil {
		logger.Errorf("Unable to update status for %s (rv = %s): %v", nameForLog, sj.ResourceVersion, err)
		return
	}
	*sj = *updatedSJ

	if sj.DeletionTimestamp != nil {
		// The TZCronJob is being deleted.
		// Don't do anything other than updating status.
		return
	}

	if sj.Spec.Suspend != nil && *sj.Spec.Suspend {
		logger.Debugf("Not starting job for %s because it is suspended", nameForLog)
		return
	}

	times, err := getRecentUnmetScheduleTimes(*sj, now)
	if err != nil {
		recorder.Eventf(sj, v1.EventTypeWarning, "FailedNeedsStart", "Cannot determine if job needs to be started: %v", err)
		logger.Errorf("Cannot determine if %s needs to be started: %v", nameForLog, err)
		return
	}
	// TODO: handle multiple unmet start times, from oldest to newest, updating status as needed.
	if len(times) == 0 {
		logger.Debugf("No unmet start times for %s", nameForLog)
		return
	}
	if len(times) > 1 {
		logger.Debugf("Multiple unmet start times for %s so only starting last one", nameForLog)
	}

	scheduledTime := times[len(times)-1]
	tooLate := false
	if sj.Spec.StartingDeadlineSeconds != nil {
		tooLate = scheduledTime.Add(time.Second * time.Duration(*sj.Spec.StartingDeadlineSeconds)).Before(now)
	}
	if tooLate {
		logger.Debugf("Missed starting window for %s", nameForLog)
		recorder.Eventf(sj, v1.EventTypeWarning, "MissSchedule", "Missed scheduled time to start a job: %s", scheduledTime.Format(time.RFC1123Z))
		// TODO: Since we don't set LastScheduleTime when not scheduling, we are going to keep noticing
		// the miss every cycle.  In order to avoid sending multiple events, and to avoid processing
		// the sj again and again, we could set a Status.LastMissedTime when we notice a miss.
		// Then, when we call getRecentUnmetScheduleTimes, we can take max(creationTimestamp,
		// Status.LastScheduleTime, Status.LastMissedTime), and then so we won't generate
		// and event the next time we process it, and also so the user looking at the status
		// can see easily that there was a missed execution.
		return
	}
	if sj.Spec.ConcurrencyPolicy == cronjobberv1.ForbidConcurrent && len(sj.Status.Active) > 0 {
		// Regardless which source of information we use for the set of active jobs,
		// there is some risk that we won't see an active job when there is one.
		// (because we haven't seen the status update to the SJ or the created pod).
		// So it is theoretically possible to have concurrency with Forbid.
		// As long the as the invocations are "far enough apart in time", this usually won't happen.
		//
		// TODO: for Forbid, we could use the same name for every execution, as a lock.
		// With replace, we could use a name that is deterministic per execution time.
		// But that would mean that you could not inspect prior successes or failures of Forbid jobs.
		logger.Debugf("Not starting job for %s because of prior execution still running and concurrency policy is Forbid", nameForLog)
		return
	}
	if sj.Spec.ConcurrencyPolicy == cronjobberv1.ReplaceConcurrent {
		for _, j := range sj.Status.Active {
			logger.Debugf("Deleting job %s of %s that was still running at next scheduled start time", j.Name, nameForLog)

			job, err := jc.GetJob(j.Namespace, j.Name)
			if err != nil {
				recorder.Eventf(sj, v1.EventTypeWarning, "FailedGet", "Get job: %v", err)
				return
			}
			if !deleteJob(sj, job, jc, recorder, logger) {
				return
			}
		}
	}

	jobReq, err := getJobFromTemplate(sj, scheduledTime)
	if err != nil {
		logger.Errorf("Unable to make Job from template in %s: %v", nameForLog, err)
		return
	}
	jobResp, err := jc.CreateJob(sj.Namespace, jobReq)
	if err != nil {
		recorder.Eventf(sj, v1.EventTypeWarning, "FailedCreate", "Error creating job: %v", err)
		return
	}
	logger.Debugf("Created Job %s for %s", jobResp.Name, nameForLog)
	recorder.Eventf(sj, v1.EventTypeNormal, "SuccessfulCreate", "Created job %v", jobResp.Name)

	// ------------------------------------------------------------------ //

	// If this process restarts at this point (after posting a job, but
	// before updating the status), then we might try to start the job on
	// the next time.  Actually, if we re-list the SJs and Jobs on the next
	// iteration of syncAll, we might not see our own status update, and
	// then post one again.  So, we need to use the job name as a lock to
	// prevent us from making the job twice (name the job with hash of its
	// scheduled time).

	// Add the just-started job to the status list.
	ref, err := getRef(jobResp)
	if err != nil {
		logger.Warnf("Unable to make object reference for job for %s", nameForLog)
	} else {
		sj.Status.Active = append(sj.Status.Active, *ref)
	}
	sj.Status.LastScheduleTime = &metav1.Time{Time: scheduledTime}
	if _, err := sjc.UpdateStatus(sj); err != nil {
		klog.Infof("Unable to update status for %s (rv = %s): %v", nameForLog, sj.ResourceVersion, err)
	}

	return
}

// deleteJob reaps a job, deleting the job, the pods and the reference in the active list
func deleteJob(sj *cronjobberv1.TZCronJob, job *batchv1.Job, jc jobControlInterface,
	recorder record.EventRecorder, logger *zap.SugaredLogger) bool {
	nameForLog := fmt.Sprintf("%s/%s", sj.Namespace, sj.Name)

	// delete the job itself...
	if err := jc.DeleteJob(job.Namespace, job.Name); err != nil {
		recorder.Eventf(sj, v1.EventTypeWarning, "FailedDelete", "Deleted job: %v", err)
		logger.Errorf("Error deleting job %s from %s: %v", job.Name, nameForLog, err)
		return false
	}
	// ... and its reference from active list
	deleteFromActiveList(sj, job.ObjectMeta.UID)
	recorder.Eventf(sj, v1.EventTypeNormal, "SuccessfulDelete", "Deleted job %v", job.Name)

	return true
}

func getRef(object runtime.Object) (*v1.ObjectReference, error) {
	return ref.GetReference(scheme.Scheme, object)
}
