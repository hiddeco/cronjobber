/*
Copyright 2022.

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

package controllers

import (
	"context"
	"fmt"
	"sort"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	"k8s.io/client-go/tools/reference"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"

	cronjobberv1alpha1 "github.com/hiddeco/cronjobber/api/v1alpha1"
	"github.com/robfig/cron"
)

var (
	jobOwnerKey             = ".metadata.controller"
	apiGVStr                = cronjobberv1alpha1.GroupVersion.String()
	scheduledTimeAnnotation = "hidde.co.cronjobber/scheduled-at"
	nextScheduleDelta       = 100 * time.Millisecond
)

// TZCronJobReconciler reconciles a TZCronJob object
type TZCronJobReconciler struct {
	client.Client
	Scheme                  *runtime.Scheme
	now                     func() time.Time
	jobControl              jobControlInterface
	MaxConcurrentReconciles int
}

//+kubebuilder:rbac:groups=cronjobber.hidde.co,resources=tzcronjobs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=cronjobber.hidde.co,resources=tzcronjobs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=cronjobber.hidde.co,resources=tzcronjobs/finalizers,verbs=update
//+kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=batch,resources=jobs/status,verbs=get

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the TZCronJob object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.0/pkg/reconcile
func (r *TZCronJobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	// creates the in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Error(err, "unable to get config for cluster")
	}
	// creates the clientset
	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Error(err, "unable to get kube-client for cluster")
	}
	r.jobControl = realJobControl{KubeClient: kubeClient}
	r.now = time.Now

	var cronJob cronjobberv1alpha1.TZCronJob
	err = r.Get(ctx, req.NamespacedName, &cronJob)

	switch {
	case errors.IsNotFound(err):
		// may be cronjob is deleted, don't need to requeue this key
		log.V(1).Info("CronJob not found, may be it is deleted")
		return ctrl.Result{}, nil
	case err != nil:
		// for other transient apiserver error requeue with exponential backoff
		log.V(1).Info("Error while getting cronjob", "err", err)
		return ctrl.Result{}, err
	}

	var jobsToBeReconciled batchv1.JobList
	if err := r.List(ctx, &jobsToBeReconciled, client.InNamespace(req.Namespace), client.MatchingFields{jobOwnerKey: req.Name}); err != nil {
		log.Error(err, "unable to list child Jobs")
		return ctrl.Result{}, err
	}

	cronJobCopy, requeueAfter, updateStatus, err := r.syncCronJob(ctx, &cronJob, &jobsToBeReconciled, req)

	if err != nil {
		log.V(1).Info("Error reconciling cronjob", "err", err)
		if updateStatus {
			if err := r.Status().Update(ctx, cronJobCopy); err != nil {
				log.Error(err, "unable to update CronJob status")
				return ctrl.Result{}, err
			}
		}

		return ctrl.Result{}, err
	}

	if r.cleanupFinishedJobs(ctx, cronJobCopy, &jobsToBeReconciled) {
		updateStatus = true
	}

	// Update the CronJob if needed
	if updateStatus {
		if err := r.Status().Update(ctx, cronJobCopy); err != nil {
			log.Error(err, "unable to update CronJob status")
			return ctrl.Result{}, err
		}
	}

	if requeueAfter != nil {
		log.V(1).Info("Re-queuing cronjob", "cronjob", klog.KRef(cronJob.GetNamespace(), cronJob.GetName()), "requeueAfter", requeueAfter)
		return ctrl.Result{
			Requeue:      true,
			RequeueAfter: *requeueAfter,
		}, nil
	}
	log.V(1).Info("not seeing any value requeve")
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *TZCronJobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// set up a real clock, since we're not in a test

	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &batchv1.Job{}, jobOwnerKey, func(rawObj client.Object) []string {
		// grab the job object, extract the owner...
		job := rawObj.(*batchv1.Job)

		owner := metav1.GetControllerOf(job)
		if owner == nil {
			return nil
		}
		// ...make sure it's a CronJob...
		if owner.APIVersion != apiGVStr || owner.Kind != "TZCronJob" {
			return nil
		}

		// ...and if so, return it
		return []string{owner.Name}
	}); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&cronjobberv1alpha1.TZCronJob{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.MaxConcurrentReconciles}).
		Owns(&batchv1.Job{}).
		Complete(r)
}

func (r *TZCronJobReconciler) syncCronJob(
	ctx context.Context,
	cronJob *cronjobberv1alpha1.TZCronJob,
	jobs *batchv1.JobList, req ctrl.Request) (*cronjobberv1alpha1.TZCronJob, *time.Duration, bool, error) {
	log := log.FromContext(ctx)

	updateStatus := false
	cronJob = cronJob.DeepCopy()
	now, err := getCurrentTimeInZone(cronJob)
	if err != nil {
		log.V(1).Info("error while getting time in given timezone", "err", err)
		return nil, nil, updateStatus, err
	}

	childrenJobs := make(map[types.UID]bool)
	for _, j := range jobs.Items {
		childrenJobs[j.ObjectMeta.UID] = true

		found := inActiveList(*cronJob, j.ObjectMeta.UID)
		if !found && !IsJobFinished(&j) {
			var cjCopy cronjobberv1alpha1.TZCronJob
			err := r.Get(ctx, req.NamespacedName, &cjCopy)
			if err != nil {
				log.V(1).Info("error while getting cronjob", "err", err)
				return nil, nil, updateStatus, err
			}
			if inActiveList(cjCopy, j.ObjectMeta.UID) {
				cronJob = &cjCopy
				log.V(1).Info("The job is in active list", "Name", j.Name)
				continue
			}
			log.V(1).Info("Saw a job that the controller did not create or forgot", "Name", j.Name)
			// We found an unfinished job that has us as the parent, but it is not in our Active list.
			// This could happen if we crashed right after creating the Job and before updating the status,
			// or if our jobs list is newer than our cj status after a relist, or if someone intentionally created
			// a job that they wanted us to adopt.
		} else if found && IsJobFinished(&j) {
			_, status := getFinishedStatus(&j)
			deleteFromActiveList(cronJob, j.ObjectMeta.UID)
			log.V(1).Info("Saw completed job:", "Name", j.Name, "status:", status)
			updateStatus = true
		} else if IsJobFinished(&j) {
			log.V(4).Info("The job is finished", "Name", j.Name)
			// a job does not have to be in active list, as long as it is finished, we will process the timestamp
			if cronJob.Status.LastSuccessfulTime == nil {
				cronJob.Status.LastSuccessfulTime = j.Status.CompletionTime
				updateStatus = true
			}
			if j.Status.CompletionTime != nil && j.Status.CompletionTime.After(cronJob.Status.LastSuccessfulTime.Time) {
				cronJob.Status.LastSuccessfulTime = j.Status.CompletionTime
				updateStatus = true
			}
		}
	}
	// Remove any job reference from the active list if the corresponding job does not exist any more.
	// Otherwise, the cronjob may be stuck in active mode forever even though there is no matching
	// job running.
	for _, j := range cronJob.Status.Active {
		_, found := childrenJobs[j.UID]
		if found {
			log.V(4).Info("Found the children job in active list, continue")
			continue
		}
		// Explicitly try to get the job from api-server to avoid a slow watch not able to update
		// the job lister on time, giving an unwanted miss

		_, err := r.jobControl.GetJob(j.Namespace, j.Name)
		switch {
		case errors.IsNotFound(err):
			// The job is actually missing, delete from active list and schedule a new one if within
			// deadline
			log.V(1).Info("Active job went missing", "Name", j.Name)
			deleteFromActiveList(cronJob, j.UID)
			updateStatus = true
		case err != nil:
			log.V(1).Info("Error while getting job using job control", "err", err)
			return cronJob, nil, updateStatus, err
		}
		// the job is missing in the lister but found in api-server
	}
	if cronJob.DeletionTimestamp != nil {
		// The CronJob is being deleted.
		// Don't do anything other than updating status.
		log.V(1).Info("The cronjob is being deleted")
		return cronJob, nil, updateStatus, nil
	}

	if cronJob.Spec.Suspend != nil && *cronJob.Spec.Suspend {
		log.V(1).Info("Not starting job because the cron is suspended")
		return cronJob, nil, updateStatus, nil
	}

	sched, err := cron.ParseStandard(cronJob.Spec.Schedule)
	if err != nil {
		// this is likely a user error in defining the spec value
		// we should log the error and not reconcile this cronjob until an update to spec
		log.V(1).Info("Unparseable schedule", "schedule", cronJob.Spec.Schedule, "err", err)
		return cronJob, nil, updateStatus, err
	}

	scheduledTime, err := getNextScheduleTime(*cronJob, now, sched)
	if err != nil {
		// this is likely a user error in defining the spec value
		// we should log the error and not reconcile this cronjob until an update to spec
		log.V(1).Info("Invalid schedule", "schedule", cronJob.Spec.Schedule, "err", err)
		return cronJob, nil, updateStatus, err
	}
	if scheduledTime == nil {
		// no unmet start time, return cj,.
		// The only time this should happen is if queue is filled after restart.
		// Otherwise, the queue is always suppose to trigger sync function at the time of
		// the scheduled time, that will give atleast 1 unmet time schedule
		log.V(4).Info("No unmet start times")
		t := nextScheduledTimeDuration(sched, now)
		return cronJob, t, updateStatus, nil
	}

	tooLate := false
	if cronJob.Spec.StartingDeadlineSeconds != nil {
		tooLate = scheduledTime.Add(time.Second * time.Duration(*cronJob.Spec.StartingDeadlineSeconds)).Before(now)
	}
	if tooLate {
		log.V(1).Info("Missed starting window", "cronjob", klog.KRef(cronJob.GetNamespace(), cronJob.GetName()))

		// TODO: Since we don't set LastScheduleTime when not scheduling, we are going to keep noticing
		// the miss every cycle.  In order to avoid sending multiple events, and to avoid processing
		// the cj again and again, we could set a Status.LastMissedTime when we notice a miss.
		// Then, when we call getRecentUnmetScheduleTimes, we can take max(creationTimestamp,
		// Status.LastScheduleTime, Status.LastMissedTime), and then so we won't generate
		// and event the next time we process it, and also so the user looking at the status
		// can see easily that there was a missed execution.
		t := nextScheduledTimeDuration(sched, now)
		return cronJob, t, updateStatus, nil
	}
	if isJobInActiveList(&batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getJobName(cronJob, *scheduledTime),
			Namespace: cronJob.Namespace,
		}}, cronJob.Status.Active) || cronJob.Status.LastScheduleTime.Equal(&metav1.Time{Time: *scheduledTime}) {
		log.V(1).Info("Not starting job because the scheduled time is already processed", "cronjob", klog.KRef(cronJob.GetNamespace(), cronJob.GetName()), "schedule", scheduledTime)
		t := nextScheduledTimeDuration(sched, now)
		return cronJob, t, updateStatus, nil
	}
	if cronJob.Spec.ConcurrencyPolicy == batchv1.ForbidConcurrent && len(cronJob.Status.Active) > 0 {
		// Regardless which source of information we use for the set of active jobs,
		// there is some risk that we won't see an active job when there is one.
		// (because we haven't seen the status update to the SJ or the created pod).
		// So it is theoretically possible to have concurrency with Forbid.
		// As long the as the invocations are "far enough apart in time", this usually won't happen.
		//
		// TODO: for Forbid, we could use the same name for every execution, as a lock.
		// With replace, we could use a name that is deterministic per execution time.
		// But that would mean that you could not inspect prior successes or failures of Forbid jobs.
		log.V(1).Info("Not starting job because prior execution is still running and concurrency policy is Forbid")

		t := nextScheduledTimeDuration(sched, now)
		return cronJob, t, updateStatus, nil
	}
	if cronJob.Spec.ConcurrencyPolicy == batchv1.ReplaceConcurrent {
		for _, j := range cronJob.Status.Active {
			log.V(1).Info("Deleting job that was still running at next scheduled start time", "job", klog.KRef(j.Namespace, j.Name))

			job, err := r.jobControl.GetJob(j.Namespace, j.Name)
			if err != nil {
				return cronJob, nil, updateStatus, err
			}
			if !deleteJob(cronJob, job, r.jobControl) {
				return cronJob, nil, updateStatus, fmt.Errorf("could not replace job: Namespace: %s, Name: %s", job.Namespace, job.Name)
			}
			updateStatus = true
		}
	}
	jobReq, err := getJobFromTemplate2(cronJob, *scheduledTime, r.Scheme)
	if err != nil {
		log.Error(err, "Unable to make Job from template", "cronjob", klog.KRef(cronJob.GetNamespace(), cronJob.GetName()))
		return cronJob, nil, updateStatus, err
	}
	jobResp, err := r.jobControl.CreateJob(cronJob.Namespace, jobReq)
	switch {
	case errors.HasStatusCause(err, corev1.NamespaceTerminatingCause):
	case errors.IsAlreadyExists(err):
		// If the job is created by other actor, assume  it has updated the cronjob status accordingly
		log.V(1).Info("Job already exists", "cronjob", klog.KRef(cronJob.GetNamespace(), cronJob.GetName()), "job", klog.KRef(jobReq.GetNamespace(), jobReq.GetName()))
		return cronJob, nil, updateStatus, err
	case err != nil:
		// default error handling
		log.V(1).Info("Error while creating job", "err", err)
		return cronJob, nil, updateStatus, err
	}

	log.V(1).Info("Created Job", "job", klog.KRef(jobResp.GetNamespace(), jobResp.GetName()), "cronjob", klog.KRef(cronJob.GetNamespace(), cronJob.GetName()))

	// ------------------------------------------------------------------ //

	// If this process restarts at this point (after posting a job, but
	// before updating the status), then we might try to start the job on
	// the next time.  Actually, if we re-list the SJs and Jobs on the next
	// iteration of syncAll, we might not see our own status update, and
	// then post one again.  So, we need to use the job name as a lock to
	// prevent us from making the job twice (name the job with hash of its
	// scheduled time).

	jobRef, err := getRef(jobResp)
	if err != nil {
		log.V(1).Info("Unable to make object reference", "cronjob", klog.KRef(cronJob.GetNamespace(), cronJob.GetName()), "err", err)
		return cronJob, nil, updateStatus, fmt.Errorf("unable to make object reference for job for %s", klog.KRef(cronJob.GetNamespace(), cronJob.GetName()))
	}
	cronJob.Status.Active = append(cronJob.Status.Active, *jobRef)
	cronJob.Status.LastScheduleTime = &metav1.Time{Time: *scheduledTime}
	updateStatus = true

	t := nextScheduledTimeDuration(sched, now)
	return cronJob, t, updateStatus, nil
}

func getJobName(cj *cronjobberv1alpha1.TZCronJob, scheduledTime time.Time) string {
	return fmt.Sprintf("%s-%d", cj.Name, getTimeHashInMinutes(scheduledTime))
}

// deleteJob reaps a job, deleting the job, the pods and the reference in the active list
func deleteJob(cj *cronjobberv1alpha1.TZCronJob, job *batchv1.Job, jc jobControlInterface) bool {
	nameForLog := fmt.Sprintf("%s/%s", cj.Namespace, cj.Name)

	// delete the job itself...
	if err := jc.DeleteJob(job.Namespace, job.Name); err != nil {
		klog.Errorf("Error deleting job %s from %s: %v", job.Name, nameForLog, err)
		return false
	}
	// ... and its reference from active list
	deleteFromActiveList(cj, job.ObjectMeta.UID)
	return true
}
func getRef(object runtime.Object) (*corev1.ObjectReference, error) {
	return reference.GetReference(scheme.Scheme, object)
}

// cleanupFinishedJobs cleanups finished jobs created by a CronJob
// It returns a bool to indicate an update to api-server is needed
func (jm *TZCronJobReconciler) cleanupFinishedJobs(ctx context.Context, cj *cronjobberv1alpha1.TZCronJob, js *batchv1.JobList) bool {
	// If neither limits are active, there is no need to do anything.
	if cj.Spec.FailedJobsHistoryLimit == nil && cj.Spec.SuccessfulJobsHistoryLimit == nil {
		return false
	}

	updateStatus := false
	failedJobs := []batchv1.Job{}
	successfulJobs := []batchv1.Job{}

	for _, job := range js.Items {
		isFinished, finishedStatus := jm.getFinishedStatus(&job)
		if isFinished && finishedStatus == batchv1.JobComplete {
			successfulJobs = append(successfulJobs, job)
		} else if isFinished && finishedStatus == batchv1.JobFailed {
			failedJobs = append(failedJobs, job)
		}
	}

	if cj.Spec.SuccessfulJobsHistoryLimit != nil &&
		jm.removeOldestJobs(cj,
			successfulJobs,
			*cj.Spec.SuccessfulJobsHistoryLimit) {
		updateStatus = true
	}

	if cj.Spec.FailedJobsHistoryLimit != nil &&
		jm.removeOldestJobs(cj,
			failedJobs,
			*cj.Spec.FailedJobsHistoryLimit) {
		updateStatus = true
	}

	return updateStatus
}

func (jm *TZCronJobReconciler) getFinishedStatus(j *batchv1.Job) (bool, batchv1.JobConditionType) {
	for _, c := range j.Status.Conditions {
		if (c.Type == batchv1.JobComplete || c.Type == batchv1.JobFailed) && c.Status == corev1.ConditionTrue {
			return true, c.Type
		}
	}
	return false, ""
}

// removeOldestJobs removes the oldest jobs from a list of jobs
func (jm *TZCronJobReconciler) removeOldestJobs(cj *cronjobberv1alpha1.TZCronJob, js []batchv1.Job, maxJobs int32) bool {
	log := log.FromContext(context.Background())
	updateStatus := false
	numToDelete := len(js) - int(maxJobs)
	if numToDelete <= 0 {
		return updateStatus
	}
	log.V(4).Info("Cleaning up jobs from CronJob list", "deletejobnum", numToDelete, "jobnum", len(js), "cronjob", klog.KRef(cj.GetNamespace(), cj.GetName()))
	sort.Sort(byJobStartTimeStar(js))
	for i := 0; i < numToDelete; i++ {
		if deleteJob(cj, &js[i], jm.jobControl) {
			updateStatus = true
		}
	}
	return updateStatus
}
