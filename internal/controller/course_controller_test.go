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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sailv1alpha1 "github.com/kingletas/oiax/api/v1alpha1"
)

var _ = Describe("Course Controller", func() {
	const (
		namespace = "default"
		// The annotation is spelled out rather than borrowed from the controller,
		// so renaming it there is a change a test notices.
		pageHash = "sail.oiax.example.com/page-hash"
	)

	ctx := context.Background()

	// Each test gets a Course of its own, so nothing one test leaves behind can
	// make another pass. envtest runs no garbage collector, so owned objects
	// stay after their Course is deleted; a unique name keeps them apart.
	var key types.NamespacedName
	var reconciler *CourseReconciler

	newCourse := func(name, message string, replicas int32) *sailv1alpha1.Course {
		return &sailv1alpha1.Course{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec:       sailv1alpha1.CourseSpec{Message: message, Replicas: new(replicas)},
		}
	}

	reconcileOnce := func() {
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
	}

	fetch := func(object client.Object) {
		Expect(k8sClient.Get(ctx, key, object)).To(Succeed())
	}

	BeforeEach(func() {
		reconciler = &CourseReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
	})

	AfterEach(func() {
		course := &sailv1alpha1.Course{}
		if err := k8sClient.Get(ctx, key, course); err == nil {
			Expect(k8sClient.Delete(ctx, course)).To(Succeed())
		}
	})

	It("serves the message from a ConfigMap, a Deployment and a Service it owns", func() {
		key = types.NamespacedName{Name: "harbour", Namespace: namespace}
		Expect(k8sClient.Create(ctx, newCourse(key.Name, "Hello from the harbour", 2))).To(Succeed())

		reconcileOnce()

		page := &corev1.ConfigMap{}
		fetch(page)
		Expect(page.Data).To(HaveKeyWithValue("index.html", "Hello from the harbour\n"))

		deployment := &appsv1.Deployment{}
		fetch(deployment)
		Expect(deployment.Spec.Replicas).To(HaveValue(Equal(int32(2))))
		Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(1))
		Expect(deployment.Spec.Template.Spec.Containers[0].Image).To(Equal("nginxinc/nginx-unprivileged:1.30-alpine"))
		Expect(deployment.Spec.Template.Spec.Volumes[0].ConfigMap.Name).To(Equal(page.Name))

		service := &corev1.Service{}
		fetch(service)
		Expect(service.Spec.Selector).To(Equal(deployment.Spec.Template.Labels))
		Expect(service.Spec.Ports[0].Port).To(Equal(int32(80)))

		for _, owned := range []metav1.Object{page, deployment, service} {
			Expect(owned.GetOwnerReferences()).To(ConsistOf(
				HaveField("Name", key.Name),
			), "%s should be owned by its Course", owned.GetName())
		}
	})

	It("updates the page and rolls the pods when the message changes", func() {
		key = types.NamespacedName{Name: "lighthouse", Namespace: namespace}
		Expect(k8sClient.Create(ctx, newCourse(key.Name, "First light", 1))).To(Succeed())
		reconcileOnce()

		before := &appsv1.Deployment{}
		fetch(before)
		hashBefore := before.Spec.Template.Annotations[pageHash]
		Expect(hashBefore).NotTo(BeEmpty())

		course := &sailv1alpha1.Course{}
		fetch(course)
		course.Spec.Message = "Second light"
		Expect(k8sClient.Update(ctx, course)).To(Succeed())
		reconcileOnce()

		page := &corev1.ConfigMap{}
		fetch(page)
		Expect(page.Data).To(HaveKeyWithValue("index.html", "Second light\n"))

		after := &appsv1.Deployment{}
		fetch(after)
		Expect(after.Spec.Template.Annotations[pageHash]).NotTo(Equal(hashBefore))
	})

	It("brings back a Deployment that was deleted", func() {
		key = types.NamespacedName{Name: "anchor", Namespace: namespace}
		Expect(k8sClient.Create(ctx, newCourse(key.Name, "Holding fast", 1))).To(Succeed())
		reconcileOnce()

		deployment := &appsv1.Deployment{}
		fetch(deployment)
		Expect(k8sClient.Delete(ctx, deployment)).To(Succeed())
		Expect(errors.IsNotFound(k8sClient.Get(ctx, key, &appsv1.Deployment{}))).To(BeTrue())

		reconcileOnce()

		fetch(&appsv1.Deployment{})
	})

	It("reports Available only once every replica asked for is ready", func() {
		key = types.NamespacedName{Name: "mooring", Namespace: namespace}
		Expect(k8sClient.Create(ctx, newCourse(key.Name, "Tied up", 2))).To(Succeed())
		reconcileOnce()

		course := &sailv1alpha1.Course{}
		fetch(course)
		available := meta.FindStatusCondition(course.Status.Conditions, "Available")
		Expect(available).NotTo(BeNil())
		Expect(available.Status).To(Equal(metav1.ConditionFalse))
		Expect(course.Status.ObservedGeneration).To(Equal(course.Generation))

		// envtest runs no Deployment controller, so the test plays its part.
		deployment := &appsv1.Deployment{}
		fetch(deployment)
		deployment.Status.Replicas = 2
		deployment.Status.ReadyReplicas = 2
		Expect(k8sClient.Status().Update(ctx, deployment)).To(Succeed())
		reconcileOnce()

		fetch(course)
		Expect(course.Status.ReadyReplicas).To(Equal(int32(2)))
		Expect(meta.IsStatusConditionTrue(course.Status.Conditions, "Available")).To(BeTrue())
	})

	It("does nothing for a Course that is gone", func() {
		key = types.NamespacedName{Name: "never-launched", Namespace: namespace}
		reconcileOnce()
	})

	It("refuses a Course that asks for more than ten replicas", func() {
		key = types.NamespacedName{Name: "armada", Namespace: namespace}
		err := k8sClient.Create(ctx, newCourse(key.Name, "Too many ships", 11))
		Expect(errors.IsInvalid(err)).To(BeTrue(), "got %v", err)
	})

	It("refuses a Course with no message", func() {
		key = types.NamespacedName{Name: "silent", Namespace: namespace}
		err := k8sClient.Create(ctx, newCourse(key.Name, "", 1))
		Expect(errors.IsInvalid(err)).To(BeTrue(), "got %v", err)
	})
})
