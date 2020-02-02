package reaper

import (
	"context"
	"fmt"
	"gopkg.in/yaml.v2"
	appsv1 "k8s.io/api/apps/v1"
	"strconv"
	"strings"
	"time"

	"github.com/jsanda/reaper-operator/pkg/apis/reaper/v1alpha1"
	v1batch "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_reaper")

const (
	ReaperImage = "jsanda/cassandra-reaper-k8s:2.0.2-b6bfb774ccbb"
)

/**
* USER ACTION REQUIRED: This is a scaffold file intended for the user to modify with their own Controller
* business logic.  Delete these comments after modifying this file.*
 */

// Add creates a new Reaper Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileReaper{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("reaper-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource Reaper
	err = c.Watch(&source.Kind{Type: &v1alpha1.Reaper{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileReaper implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileReaper{}

// ReconcileReaper reconciles a Reaper object
type ReconcileReaper struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a Reaper object and makes changes based on the state read
// and what is in the Reaper.Spec
// TODO(user): Modify this Reconcile function to implement your Controller logic.  This example creates
// a Pod as an example
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileReaper) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling Reaper")

	// Fetch the Reaper instance
	instance := &v1alpha1.Reaper{}
	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{Requeue: true, RequeueAfter: 30 * time.Second}, err
	}

	instance = instance.DeepCopy()

	if len(instance.Status.Conditions) == 0  {
		reqLogger.Info("Checking defaults")
		if checkDefaults(instance) {
			if err = r.client.Update(context.TODO(), instance); err != nil {
				return reconcile.Result{}, err
			}
			return reconcile.Result{Requeue: true}, nil
		}

		reqLogger.Info("Reconciling configmap")
		serverConfig := &corev1.ConfigMap{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: instance.Name, Namespace: instance.Namespace}, serverConfig)
		if err != nil && errors.IsNotFound(err) {
			// create server config configmap
			cm, err := r.newServerConfigMap(instance)
			if err != nil {
				reqLogger.Error(err, "Failed to create new ConfigMap")
				return reconcile.Result{}, err
			}
			reqLogger.Info("Creating configmap", "Reaper.Namespace", instance.Namespace, "Reaper.Name",
				instance.Name, "ConfigMap.Name", cm.Name)
			if err = controllerutil.SetControllerReference(instance, cm, r.scheme); err != nil {
				reqLogger.Error(err, "Failed to set owner reference on Reaper server config ConfigMap")
				return reconcile.Result{}, err
			}
			if err = r.client.Create(context.TODO(), cm); err != nil {
				reqLogger.Error(err, "Failed to save ConfigMap")
				return reconcile.Result{}, err
			} else {
				return reconcile.Result{Requeue: true, RequeueAfter: 5 * time.Second}, nil
			}
		} else if err != nil {
			reqLogger.Error(err, "Failed to get ConfigMap")
			return reconcile.Result{}, err
		}

		reqLogger.Info("Reconciling service")
		service := &corev1.Service{}
		err = r.client.Get(context.TODO(),types.NamespacedName{Name: instance.Name, Namespace: instance.Namespace}, service)
		if err != nil && errors.IsNotFound(err) {
			// Create the Service
			service = r.newService(instance)
			reqLogger.Info("Creating service", "Reaper.Namespace", instance.Namespace, "Reaper.Name",
				instance.Name, "Service.Name", service.Name)
			if err = controllerutil.SetControllerReference(instance, service, r.scheme); err != nil {
				reqLogger.Error(err, "Failed to set owner reference on Reaper Service")
				return reconcile.Result{}, err
			}
			if err = r.client.Create(context.TODO(), service); err != nil {
				reqLogger.Error(err, "Failed to create Service")
				return reconcile.Result{}, err
			} else {
				return reconcile.Result{Requeue: true}, nil
			}
		} else if err != nil {
			reqLogger.Error(err, "Failed to get Service")
			return reconcile.Result{}, err
		}

		if instance.Spec.ServerConfig.StorageType == v1alpha1.Cassandra {
			reqLogger.Info("Reconciling schema job")
			schemaJob := &v1batch.Job{}
			jobName := getSchemaJobName(instance)
			err = r.client.Get(context.TODO(), types.NamespacedName{Namespace: instance.Namespace, Name: jobName}, schemaJob)
			if err != nil && errors.IsNotFound(err) {
				// Create the job
				schemaJob = r.newSchemaJob(instance)
				reqLogger.Info("Creating schema job", "Reaper.Namespace", instance.Namespace, "Reaper.Name",
					instance.Name, "Job.Name", schemaJob.Name)
				if err = controllerutil.SetControllerReference(instance, schemaJob, r.scheme); err != nil {
					reqLogger.Error(err, "Failed to set owner reference", "SchemaJob", jobName)
					return reconcile.Result{}, err
				}
				if err = r.client.Create(context.TODO(), schemaJob); err != nil {
					reqLogger.Error(err, "Failed to create schema Job")
					return reconcile.Result{}, err
				} else {
					return reconcile.Result{Requeue: true, RequeueAfter: 5 * time.Second}, nil
				}
			} else if err != nil {
				reqLogger.Error(err, "Failed to get schema Job")
				return reconcile.Result{}, err
			} else if !jobFinished(schemaJob) {
				return reconcile.Result{Requeue: true, RequeueAfter: 5 * time.Second}, nil
			} else if failed, err := jobFailed(schemaJob); failed {
				return reconcile.Result{}, err
			} // else the job has completed successfully
		}

		reqLogger.Info("Reconciling deployment")
		deployment := &appsv1.Deployment{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: instance.Name, Namespace: instance.Namespace}, deployment)
		if err != nil && errors.IsNotFound(err) {
			// Create the Deployment
			deployment = r.newDeployment(instance)
			reqLogger.Info("Creating deployment", "Reaper.Namespace", instance.Namespace, "Reaper.Name",
				instance.Name, "Deployment.Name", deployment.Name)
			if err = controllerutil.SetControllerReference(instance, deployment, r.scheme); err != nil {
				reqLogger.Error(err, "Failed to set owner reference on Reaper Deployment")
				return reconcile.Result{}, err
			}
			if err = r.client.Create(context.TODO(), deployment); err != nil {
				reqLogger.Error(err, "Failed to create Deployment")
				return reconcile.Result{}, err
			} else {
				return reconcile.Result{Requeue: true, RequeueAfter: 5 * time.Second}, nil
			}
		} else if err != nil {
			reqLogger.Error(err, "Failed to get Deployment")
			return reconcile.Result{}, err
		}

		if updateStatus(instance, deployment) {
			if err = r.client.Status().Update(context.TODO(), instance); err != nil {
				reqLogger.Error(err, "Failed to update status")
				return reconcile.Result{}, err
			}
		}

		if instance.Status.ReadyReplicas != instance.Status.Replicas {
			return reconcile.Result{Requeue: true, RequeueAfter: 5 * time.Second}, nil
		}
	}

	return reconcile.Result{}, nil
}

func (r *ReconcileReaper) newServerConfigMap(instance *v1alpha1.Reaper) (*corev1.ConfigMap, error) {
	output, err := yaml.Marshal(&instance.Spec.ServerConfig)
	if err != nil {
		return nil, err
	}

	cm := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			Kind: "ConfigMap",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: instance.Name,
			Namespace: instance.Namespace,
		},
		Data: map[string]string{
			"reaper.yaml": string(output),
		},
	}

	return cm, nil
}

func checkDefaults(instance *v1alpha1.Reaper) bool {
	updated := false

	if instance.Spec.ServerConfig.HangingRepairTimeoutMins == nil {
		instance.Spec.ServerConfig.HangingRepairTimeoutMins = int32Ptr(v1alpha1.DefaultHangingRepairTimeoutMins)
		updated = true
	}

	if instance.Spec.ServerConfig.RepairIntensity == "" {
		instance.Spec.ServerConfig.RepairIntensity = v1alpha1.DefaultRepairIntensity
		updated = true
	}

	if instance.Spec.ServerConfig.RepairParallelism == "" {
		instance.Spec.ServerConfig.RepairParallelism = v1alpha1.DefaultRepairParallelism
		updated = true
	}

	if instance.Spec.ServerConfig.RepairRunThreadCount == nil {
		instance.Spec.ServerConfig.RepairRunThreadCount = int32Ptr(v1alpha1.DefaultRepairRunThreadCount)
		updated = true
	}

	if instance.Spec.ServerConfig.ScheduleDaysBetween == nil {
		instance.Spec.ServerConfig.ScheduleDaysBetween = int32Ptr(v1alpha1.DefaultScheduleDaysBetween)
		updated = true
	}

	if instance.Spec.ServerConfig.StorageType == "" {
		instance.Spec.ServerConfig.StorageType = v1alpha1.DefaultStorageType
		updated = true
	}

	if instance.Spec.ServerConfig.EnableCrossOrigin == nil {
		instance.Spec.ServerConfig.EnableCrossOrigin = boolPtr(v1alpha1.DefaultEnableCrossOrigin)
		updated = true
	}

	if instance.Spec.ServerConfig.EnableDynamicSeedList == nil {
		instance.Spec.ServerConfig.EnableDynamicSeedList = boolPtr(v1alpha1.DefaultEnableDynamicSeedList)
		updated = true
	}

	if instance.Spec.ServerConfig.JmxConnectionTimeoutInSeconds == nil {
		instance.Spec.ServerConfig.JmxConnectionTimeoutInSeconds = int32Ptr(v1alpha1.DefaultJmxConnectionTimeoutInSeconds)
		updated = true
	}

	if instance.Spec.ServerConfig.SegmentCountPerNode == nil {
		instance.Spec.ServerConfig.SegmentCountPerNode = int32Ptr(v1alpha1.DefaultSegmentCountPerNode)
		updated = true
	}

	if instance.Spec.ServerConfig.CassandraBackend == nil {
		instance.Spec.ServerConfig.CassandraBackend = &v1alpha1.CassandraBackend{
			AuthProvider: v1alpha1.AuthProvider{
				Type: "plainText",
				Username: "cassandra",
				Password: "cassandra",
			},
		}
		updated = true
	}

	return updated
}

func updateStatus(instance *v1alpha1.Reaper, deployment *appsv1.Deployment) bool {
	updated := false

	if instance.Status.AvailableReplicas != deployment.Status.AvailableReplicas {
		instance.Status.AvailableReplicas = deployment.Status.AvailableReplicas
		updated = true
	}

	if instance.Status.ReadyReplicas != deployment.Status.ReadyReplicas {
		instance.Status.ReadyReplicas = deployment.Status.ReadyReplicas
		updated = true
	}

	if instance.Status.Replicas != deployment.Status.Replicas {
		instance.Status.Replicas = deployment.Status.Replicas
		updated = true
	}

	if instance.Status.UpdatedReplicas != deployment.Status.UpdatedReplicas {
		instance.Status.UpdatedReplicas = deployment.Status.UpdatedReplicas
		updated = true
	}

	return updated
}

func (r *ReconcileReaper) newService(instance *v1alpha1.Reaper) *corev1.Service {
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind: "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: instance.Name,
			Namespace: instance.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Port: 8080,
					Name: "ui",
					Protocol: corev1.ProtocolTCP,
					TargetPort: intstr.IntOrString{
						Type: intstr.String,
						StrVal: "ui",
					},
				},
			},
			Selector: createLabels(instance),
		},
	}
}

func (r *ReconcileReaper) newSchemaJob(instance *v1alpha1.Reaper) *v1batch.Job {
	cassandra := *instance.Spec.ServerConfig.CassandraBackend
	return &v1batch.Job{
		TypeMeta: metav1.TypeMeta{
			Kind: "Job",
			APIVersion: "batch/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
				Namespace: instance.Namespace,
				Name: getSchemaJobName(instance),
		},
		Spec: v1batch.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyOnFailure,
					Containers: []corev1.Container{
						{
							Name: getSchemaJobName(instance),
							Image: "jsanda/create_keyspace:latest",
							ImagePullPolicy: corev1.PullIfNotPresent,
							Env: []corev1.EnvVar{
								{
									Name: "KEYSPACE",
									Value: cassandra.Keyspace,
								},
								{
									Name: "CONTACT_POINTS",
									Value: strings.Join(cassandra.ContactPoints, ","),
								},
								// TODO Add replication_factor. There is already a function in tlp-stress-operator
								//      that does the serialization. I need to move that function to a shared lib.
								{
									Name: "REPLICATION",
									Value: convert(cassandra.Replication),
								},
							},
						},
					},
				},
			},
		},
	}
}

func convert(r v1alpha1.ReplicationConfig) string {
	if r.SimpleStrategy != nil {
		replicationFactor := strconv.FormatInt(int64(*r.SimpleStrategy), 10)
		return fmt.Sprintf(`{'class': 'SimpleStrategy', 'replication_factor': %s}`, replicationFactor)
	} else {
		var sb strings.Builder
		dcs := make([]string, 0)
		for k, v := range *r.NetworkTopologyStrategy {
			sb.WriteString("'")
			sb.WriteString(k)
			sb.WriteString("': ")
			sb.WriteString(strconv.FormatInt(int64(v), 10))
			dcs = append(dcs, sb.String())
			sb.Reset()
		}
		return fmt.Sprintf("{'class': 'NetworkTopologyStrategy', %s}", strings.Join(dcs, ", "))
	}
}

func getSchemaJobName(r *v1alpha1.Reaper) string {
	return fmt.Sprintf("%s-schema", r.Name)
}

func jobFinished(job *v1batch.Job) bool {
	for _, c := range job.Status.Conditions {
		if (c.Type == v1batch.JobComplete || c.Type == v1batch.JobFailed) && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func jobComplete(job *v1batch.Job) bool {
	if job.Status.CompletionTime == nil {
		return false
	}
	for _, cond := range job.Status.Conditions {
		if cond.Type == v1batch.JobComplete && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func jobFailed(job *v1batch.Job) (bool, error) {
	for _, cond := range job.Status.Conditions {
		if cond.Type == v1batch.JobFailed && cond.Status == corev1.ConditionTrue {
			return true, fmt.Errorf("schema job failed: %s", cond.Message)
		}
	}
	return false, nil
}

func (r *ReconcileReaper) newDeployment(instance *v1alpha1.Reaper) *appsv1.Deployment {
	selector := metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Key: "app",
				Operator: metav1.LabelSelectorOpIn,
				Values: []string{"reaper"},
			},
			{
				Key: "reaper",
				Operator: metav1.LabelSelectorOpIn,
				Values: []string{instance.Name},
			},
		},
	}
	return &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind: "Deployment",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: instance.Name,
			Namespace: instance.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &selector,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: createLabels(instance),
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "reaper",
							ImagePullPolicy: corev1.PullIfNotPresent,
							Image: ReaperImage,
							Ports: []corev1.ContainerPort{
								{
									Name: "ui",
									ContainerPort: 8080,
									Protocol: "TCP",
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name: "reaper-config",
									MountPath: "/etc/cassandra-reaper",
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "reaper-config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: instance.Name,
									},
									Items: []corev1.KeyToPath{
										{
											Key: "reaper.yaml",
											Path: "cassandra-reaper.yaml",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func createLabels(instance *v1alpha1.Reaper) map[string]string {
	return map[string]string{
		"app": "reaper",
		"reaper": instance.Name,
	}
}

func int32Ptr(n int32) *int32 {
	return &n
}

func boolPtr(b bool) *bool {
	return &b
}
