package reconcile

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	api "github.com/thelastpickle/reaper-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Reconciler interface {
	Reconcile(ctx context.Context, reaper *api.Reaper, log logr.Logger) (*ctrl.Result, error)
}

type deploymentReconciler struct {
	client.Client
}

func NewDeploymentReconciler(client client.Client) Reconciler {
	return &deploymentReconciler{Client: client}
}

func (r *deploymentReconciler) Reconcile(ctx context.Context, reaper *api.Reaper, log logr.Logger) (*ctrl.Result, error) {
	key := types.NamespacedName{Namespace: reaper.Namespace, Name: reaper.Name}

	log.WithValues("deployment", key)
	log.Info("reconciling deployment")

	deployment := &appsv1.Deployment{}
	err := r.Get(ctx, key, deployment)
	if err != nil && errors.IsNotFound(err) {
		deployment = newDeployment(reaper)
		log.Info("creating deployment")
		if err = r.Create(ctx, deployment); err != nil {
			log.Error(err, "failed to create deployment")
			return &ctrl.Result{Requeue: true, RequeueAfter: 10 * time.Second}, err
		} else {
			return nil, nil
		}
	}

	return nil, nil
}

func newDeployment(reaper *api.Reaper) *appsv1.Deployment {
	selector := metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Key:      "app",
				Operator: metav1.LabelSelectorOpIn,
				Values:   []string{"reaper"},
			},
			{
				Key:      "reaper",
				Operator: metav1.LabelSelectorOpIn,
				Values:   []string{reaper.Name},
			},
		},
	}

	healthProbe := &corev1.Probe{
		Handler: corev1.Handler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: "/healthcheck",
				Port: intstr.FromInt(8081),
			},
		},
		InitialDelaySeconds: 10,
		PeriodSeconds:       3,
	}

	envVars := make([]corev1.EnvVar, 0)
	if reaper.Spec.ServerConfig.CassandraBackend != nil {
		envVars = []corev1.EnvVar{
			{
				Name:  "REAPER_STORAGE_TYPE",
				Value: "cassandra",
			},
			{
				Name:  "REAPER_ENABLE_DYNAMIC_SEED_LIST",
				Value: "false",
			},
			{
				Name:  "REAPER_CASS_CONTACT_POINTS",
				Value: fmt.Sprintf("[%s]", reaper.Spec.ServerConfig.CassandraBackend.CassandraService),
			},
			//{
			//	Name:
			//},
		}
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: reaper.Namespace,
			Name:      reaper.Name,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &selector,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":    "reaper",
						"reaper": reaper.Name,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            "reaper",
							ImagePullPolicy: corev1.PullIfNotPresent,
							Image:           "thelastpickle/cassandra-reaper:2.0.5",
							Ports: []corev1.ContainerPort{
								{
									Name:          "ui",
									ContainerPort: 8080,
									Protocol:      "TCP",
								},
								{
									Name:          "admin",
									ContainerPort: 8081,
									Protocol:      "TCP",
								},
							},
							LivenessProbe:  healthProbe,
							ReadinessProbe: healthProbe,
							Env:            envVars,
						},
					},
				},
			},
		},
	}
}
