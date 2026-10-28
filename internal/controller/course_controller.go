/*
Copyright 2026.

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

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	sailv1alpha1 "github.com/kingletas/oiax/api/v1alpha1"
)

const (
	// servePort is where the web server listens inside the pod.
	servePort = 8080
	// pageDir is where the web server looks for index.html.
	pageDir = "/usr/share/nginx/html"
	// pageHashAnnotation carries a hash of the message on the pod template, so
	// a new message rolls the pods instead of waiting for them to restart.
	pageHashAnnotation = "sail.oiax.example.com/page-hash"
)

// CourseReconciler turns a Course into a ConfigMap, a Deployment and a Service.
type CourseReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=sail.oiax.example.com,resources=courses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sail.oiax.example.com,resources=courses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sail.oiax.example.com,resources=courses/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps;services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete

// Reconcile makes the cluster match one Course. It runs whenever the Course or
// anything it owns changes, and every run does the whole job from the top, so
// it does not matter which change woke it.
func (r *CourseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var course sailv1alpha1.Course
	if err := r.Get(ctx, req.NamespacedName, &course); err != nil {
		// A Course that is gone needs nothing: what it owned goes with it.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	page := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: course.Name, Namespace: course.Namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, page, func() error {
		page.Data = map[string]string{"index.html": course.Spec.Message + "\n"}
		return controllerutil.SetControllerReference(&course, page, r.Scheme)
	}); err != nil {
		return ctrl.Result{}, err
	}

	labels := map[string]string{
		"app.kubernetes.io/name":       "course",
		"app.kubernetes.io/instance":   course.Name,
		"app.kubernetes.io/managed-by": "oiax",
	}

	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: course.Name, Namespace: course.Namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, deployment, func() error {
		deployment.Spec.Replicas = course.Spec.Replicas
		deployment.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
		deployment.Spec.Template.Labels = labels
		deployment.Spec.Template.Annotations = map[string]string{pageHashAnnotation: hashOf(course.Spec.Message)}
		deployment.Spec.Template.Spec.Containers = []corev1.Container{{
			Name:  "web",
			Image: course.Spec.Image,
			Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: servePort}},
			VolumeMounts: []corev1.VolumeMount{{
				Name: "page", MountPath: pageDir, ReadOnly: true,
			}},
		}}
		deployment.Spec.Template.Spec.Volumes = []corev1.Volume{{
			Name: "page",
			VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: page.Name},
			}},
		}}
		return controllerutil.SetControllerReference(&course, deployment, r.Scheme)
	}); err != nil {
		return ctrl.Result{}, err
	}

	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: course.Name, Namespace: course.Namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, service, func() error {
		service.Spec.Selector = labels
		service.Spec.Ports = []corev1.ServicePort{{
			Name: "http", Port: 80, TargetPort: intstr.FromString("http"),
		}}
		return controllerutil.SetControllerReference(&course, service, r.Scheme)
	}); err != nil {
		return ctrl.Result{}, err
	}

	wanted := int32(1)
	if course.Spec.Replicas != nil {
		wanted = *course.Spec.Replicas
	}
	available := metav1.Condition{
		Type:               "Available",
		Status:             metav1.ConditionFalse,
		Reason:             "Progressing",
		Message:            "waiting for replicas to be ready",
		ObservedGeneration: course.Generation,
	}
	if deployment.Status.ReadyReplicas >= wanted {
		available.Status = metav1.ConditionTrue
		available.Reason = "Ready"
		available.Message = "every replica asked for is ready"
	}
	course.Status.ReadyReplicas = deployment.Status.ReadyReplicas
	course.Status.ObservedGeneration = course.Generation
	meta.SetStatusCondition(&course.Status.Conditions, available)
	if err := r.Status().Update(ctx, &course); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("reconciled", "ready", deployment.Status.ReadyReplicas, "wanted", wanted)
	return ctrl.Result{}, nil
}

// hashOf is a short, stable fingerprint of a message.
func hashOf(message string) string {
	sum := sha256.Sum256([]byte(message))
	return hex.EncodeToString(sum[:8])
}

// SetupWithManager runs Reconcile when a Course changes, and when anything a
// Course owns changes, so a deleted Deployment comes straight back.
func (r *CourseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&sailv1alpha1.Course{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Named("course").
		Complete(r)
}
