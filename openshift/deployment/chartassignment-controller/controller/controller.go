// Copyright 2019 The Cloud Robotics Authors

//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package chartassignment

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	apps "github.com/googlecloudrobotics/core/src/go/pkg/apis/apps/v1alpha1"
	"github.com/googlecloudrobotics/ilog"
	"github.com/pkg/errors"
	core "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	kclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	statusCheckingOptOutLabel = "cloudrobotics.com/opt-out-error-checking"
)

// Add adds a controller and validation webhook for the ChartAssignment resource type
// to the manager and server.
// Handled ChartAssignments are filtered by the provided cluster.
func Add(ctx context.Context, mgr manager.Manager, cloud bool, namespace string) error {
	if namespace == "" {
		return fmt.Errorf("watch namespace must not be empty")
	}
	r := &Reconciler{
		kube:      mgr.GetClient(),
		recorder:  mgr.GetEventRecorderFor("chartassignment-controller"),
		cloud:     cloud,
		namespace: namespace,
	}
	var err error
	r.releases, err = newReleases(mgr.GetConfig(), r.recorder)
	if err != nil {
		return err
	}

	c, err := controller.New("chartassignment", mgr, controller.Options{
		Reconciler: r,
	})
	if err != nil {
		return err
	}
	err = c.Watch(
		source.Kind(mgr.GetCache(), &apps.ChartAssignment{}),
		&handler.EnqueueRequestForObject{},
	)
	if err != nil {
		return err
	}
	err = c.Watch(
		source.Kind(mgr.GetCache(), &core.Pod{}),
		&handler.Funcs{
			CreateFunc: func(ctx context.Context, e event.CreateEvent, q workqueue.RateLimitingInterface) {
				r.enqueueForPod(ctx, e.Object, q)
			},
			UpdateFunc: func(ctx context.Context, e event.UpdateEvent, q workqueue.RateLimitingInterface) {
				r.enqueueForPod(ctx, e.ObjectNew, q)
			},
			DeleteFunc: func(ctx context.Context, e event.DeleteEvent, q workqueue.RateLimitingInterface) {
				r.enqueueForPod(ctx, e.Object, q)
			},
		},
	)
	if err != nil {
		return errors.Wrap(err, "watch Apps")
	}
	return nil
}

func (r *Reconciler) enqueueForPod(ctx context.Context, m meta.Object, q workqueue.RateLimitingInterface) {
	if m.GetNamespace() != r.namespace {
		return
	}
	var cas apps.ChartAssignmentList
	err := r.kube.List(ctx, &cas, kclient.InNamespace(r.namespace))
	if err != nil {
		slog.Error("List ChartAssignments failed", slog.String("Namespace", m.GetNamespace()), ilog.Err(err))
		return
	}
	for _, ca := range cas.Items {
		q.Add(reconcile.Request{
			NamespacedName: kclient.ObjectKey{Namespace: ca.Namespace, Name: ca.Name},
		})
	}
}

// Reconciler provides an idempotent function that brings the cluster into a
// state consistent with the specification of a ChartAssignment.
type Reconciler struct {
	kube      kclient.Client
	recorder  record.EventRecorder
	releases  *releases
	cloud     bool
	namespace string
}

// Reconcile creates and updates a Synk ResourceSet for the given chart
// assignment. It rolls back releases to the previous revision if an upgrade
// failed. It continuously requeues the ChartAssignment for reconciliation to
// monitor the status of the ResourceSet.
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	if req.Namespace != r.namespace {
		return reconcile.Result{}, fmt.Errorf("refusing ChartAssignment outside namespace %q", r.namespace)
	}
	var as apps.ChartAssignment
	err := r.kube.Get(ctx, req.NamespacedName, &as)

	if k8serrors.IsNotFound(err) {
		// Assignment was already deleted. We did all required cleanup
		// when removing the finalizer. Thus, there's nothing to do.
		slog.Info("ChartAssignment no longer exists, skipping reconciliation...", slog.Any("Name", req.NamespacedName))
		return reconcile.Result{}, nil
	} else if err != nil {
		return reconcile.Result{}, fmt.Errorf("getting ChartAssignment %q failed: %s", req, err)
	}
	if as.Namespace != r.namespace || as.Spec.NamespaceName != r.namespace {
		return reconcile.Result{}, fmt.Errorf("ChartAssignment %s/%s must target namespace %q", as.Namespace, as.Name, r.namespace)
	}
	// Reconcile no ChartAssignments for robots on the cloud.
	// We do have ChartAssignments without the robot label on the robot but they
	// do not pass through the cloud cluster.
	// Labels is of type map[string]string but we care only for the existence
	// of the key.
	_, isRobot := as.Labels["cloudrobotics.com/robot-name"]
	if r.cloud && isRobot {
		return reconcile.Result{}, nil
	}
	return r.reconcile(ctx, &as)
}

const (
	// The finalizer that's applied to assignments to block their garbage collection
	// until the Synk ResourceSet is deleted.
	finalizer = "helm.apps.cloudrobotics.com"
	// Requeue interval when the underlying Synk ResourceSet is not in a stable state yet.
	requeueFast = 3 * time.Second
	// Requeue interval after the underlying Synk ResourceSete reached a stable state.
	requeueSlow = 3 * time.Minute
)

func (r *Reconciler) reconcile(ctx context.Context, as *apps.ChartAssignment) (reconcile.Result, error) {
	// If we are scheduled for deletion, delete the Synk ResourceSet and drop our
	// finalizer so garbage collection can continue.
	if as.DeletionTimestamp != nil {
		slog.Info("Ensure ChartAssignment cleanup", slog.String("Name", as.Name))

		if err := r.ensureDeleted(ctx, as); err != nil {
			return reconcile.Result{}, fmt.Errorf("ensure deleted: %s", err)
		}
		if err := r.setStatus(ctx, as); err != nil {
			return reconcile.Result{}, fmt.Errorf("set status: %s", err)
		}
		// Requeue to track deletion progress.
		return reconcile.Result{Requeue: true, RequeueAfter: requeueFast}, nil
	}

	// Ensure a finalizer on the ChartAssignment so we don't get deleted before
	// we've properly deleted the associated Synk ResourceSet.
	if !stringsContain(as.Finalizers, finalizer) {
		as.Finalizers = append(as.Finalizers, finalizer)
		if err := r.kube.Update(ctx, as); err != nil {
			return reconcile.Result{}, errors.Wrap(err, "add finalizer")
		}
	}

	r.releases.ensureUpdated(as)

	if err := r.setStatus(ctx, as); err != nil {
		if k8serrors.IsConflict(err) {
			// The cache has an old status. This can be ignored, as
			// controller-runtime will reconcile again when the cache updates:
			// https://github.com/kubernetes-sigs/controller-runtime/issues/377
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, errors.Wrap(err, "update status")
	}
	// Quickly requeue for status updates when deployment is in progress.
	switch as.Status.Phase {
	case apps.ChartAssignmentPhaseReady, apps.ChartAssignmentPhaseFailed:
		return reconcile.Result{Requeue: true, RequeueAfter: requeueSlow}, nil
	}
	return reconcile.Result{Requeue: true, RequeueAfter: requeueFast}, nil

}

func condition(b bool) core.ConditionStatus {
	if b {
		return core.ConditionTrue
	}
	return core.ConditionFalse
}

func (r *Reconciler) setStatus(ctx context.Context, as *apps.ChartAssignment) error {
	status, ok := r.releases.status(as.Name)
	if !ok {
		return nil
	} else if status.phase == apps.ChartAssignmentPhaseDeleted {
		// The assignment may have been garbage collected already, so
		// don't try to update the status.
		return nil
	}

	as.Status.ObservedGeneration = as.Generation
	as.Status.Phase = status.phase

	if c := condition(status.phase == apps.ChartAssignmentPhaseSettled); status.err == nil {
		setCondition(as, apps.ChartAssignmentConditionSettled, c, "")
	} else {
		setCondition(as, apps.ChartAssignmentConditionSettled, c, status.err.Error())
	}

	// ChartAssignments are namespace-scoped and the namespace is enforced before
	// reconciliation. Read readiness only from that namespace.
	var pods core.PodList
	if err := r.kube.List(ctx, &pods, kclient.InNamespace(r.namespace)); err != nil {
		return errors.Wrap(err, "list pods")
	}

	// Omit pods that have opted out of status checking.
	var filteredPods []core.Pod
	for _, pod := range pods.Items {
		if val, exists := pod.Labels[statusCheckingOptOutLabel]; !exists || val != "true" {
			filteredPods = append(filteredPods, pod)
		}
	}
	ready, total := 0, len(filteredPods)
	for _, pod := range filteredPods {
		if pod.Status.Phase == core.PodRunning || pod.Status.Phase == core.PodSucceeded {
			ready++
		}
	}
	if status.phase != apps.ChartAssignmentPhaseSettled {
		setCondition(as, apps.ChartAssignmentConditionReady, condition(false), "Release not settled yet")
	} else {
		if ready == total {
			as.Status.Phase = apps.ChartAssignmentPhaseReady
		}
		setCondition(as, apps.ChartAssignmentConditionReady, condition(ready == total),
			fmt.Sprintf("%d/%d pods are running or succeeded", ready, total))
	}

	return r.kube.Status().Update(ctx, as)
}

// ensureDeleted ensures that the Synk ResourceSet is deleted and the finalizer gets removed.
func (r *Reconciler) ensureDeleted(ctx context.Context, as *apps.ChartAssignment) error {
	r.releases.ensureDeleted(as)
	status, ok := r.releases.status(as.Name)
	if !ok {
		return fmt.Errorf("release status not found")
	}

	if status.phase != apps.ChartAssignmentPhaseDeleted {
		// Deletion still in progress, check again later.
		return nil
	}
	if !stringsContain(as.Finalizers, finalizer) {
		return nil
	}
	as.Finalizers = stringsDelete(as.Finalizers, finalizer)
	if err := r.kube.Update(ctx, as); err != nil {
		return fmt.Errorf("update failed: %s", err)
	}
	return nil
}

func stringsContain(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func stringsDelete(list []string, s string) (res []string) {
	for _, x := range list {
		if x != s {
			res = append(res, x)
		}
	}
	return res
}

// setOwnerReference ensures the owner reference is set and returns true if it did
// not exist before. Existing references are detected based on the UID field.
func inCondition(as *apps.ChartAssignment, c apps.ChartAssignmentConditionType) bool {
	for _, cond := range as.Status.Conditions {
		if cond.Type == c && cond.Status == core.ConditionTrue {
			return true
		}
	}
	return false
}

// setCondition adds or updates a condition. Existing conditions are detected
// based on the Type field.
func setCondition(as *apps.ChartAssignment, t apps.ChartAssignmentConditionType, v core.ConditionStatus, msg string) {
	now := meta.Now()

	for i, c := range as.Status.Conditions {
		if c.Type != t {
			continue
		}
		// Update existing condition.
		if c.Status != v || c.Message != msg {
			c.LastUpdateTime = now
		}
		if c.Status != v {
			c.LastTransitionTime = now
		}
		c.Message = msg
		c.Status = v
		as.Status.Conditions[i] = c
		return
	}
	// Condition set for the first time.
	as.Status.Conditions = append(as.Status.Conditions, apps.ChartAssignmentCondition{
		Type:               t,
		LastUpdateTime:     now,
		LastTransitionTime: now,
		Status:             v,
		Message:            msg,
	})
}
