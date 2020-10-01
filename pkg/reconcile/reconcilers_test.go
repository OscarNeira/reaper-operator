package reconcile

import (
	"testing"

	"github.com/stretchr/testify/assert"
	api "github.com/thelastpickle/reaper-operator/api/v1alpha1"
	"github.com/thelastpickle/reaper-operator/pkg/config"
	mlabels "github.com/thelastpickle/reaper-operator/pkg/labels"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestNewSchemaJob(t *testing.T) {
	namespace := "schema-job-test"
	reaperName := "test-reaper"
	clusterName := "cassandra"
	cassandraService := "cassandra-svc"

	reaper := &api.Reaper{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      reaperName,
		},
		Spec: api.ReaperSpec{
			ServerConfig: api.ServerConfig{
				StorageType: api.StorageTypeCassandra,
				CassandraBackend: &api.CassandraBackend{
					ClusterName:      clusterName,
					CassandraService: cassandraService,
					Keyspace:         api.DefaultKeyspace,
					Replication: api.ReplicationConfig{
						NetworkTopologyStrategy: &map[string]int32{
							"DC1": 3,
						},
					},
				},
			},
		},
	}

	job := newSchemaJob(reaper)

	assert.Equal(t, getSchemaJobName(reaper), job.Name)
	assert.Equal(t, namespace, job.Namespace)
	assert.Equal(t, createLabels(reaper), job.Labels)

	podSpec := job.Spec.Template.Spec
	assert.Equal(t, corev1.RestartPolicyOnFailure, podSpec.RestartPolicy)
	assert.Equal(t, 1, len(podSpec.Containers))

	container := podSpec.Containers[0]
	assert.Equal(t, schemaJobImage, container.Image)
	assert.Equal(t, schemaJobImagePullPolicy, container.ImagePullPolicy)
	assert.ElementsMatch(t, container.Env, []corev1.EnvVar{
		{
			Name:  "KEYSPACE",
			Value: api.DefaultKeyspace,
		},
		{
			Name:  "CONTACT_POINTS",
			Value: cassandraService,
		},
		{
			Name:  "REPLICATION",
			Value: config.ReplicationToString(reaper.Spec.ServerConfig.CassandraBackend.Replication),
		},
	})
}

func TestNewDeployment(t *testing.T) {
	namespace := "deployment-test"
	reaperName := "test-reaper"
	image := "test/reaper:latest"
	clusterName := "cassandra"
	cassandraService := "cassandra-svc"

	reaper := &api.Reaper{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      reaperName,
		},
		Spec: api.ReaperSpec{
			Image: image,
			ServerConfig: api.ServerConfig{
				StorageType: api.StorageTypeCassandra,
				CassandraBackend: &api.CassandraBackend{
					ClusterName:      clusterName,
					CassandraService: cassandraService,
				},
			},
		},
	}

	labels := createLabels(reaper)
	deployment := newDeployment(reaper)

	assert.Equal(t, namespace, deployment.Namespace)
	assert.Equal(t, reaperName, deployment.Name)
	assert.Equal(t, labels, deployment.Labels)

	selector := deployment.Spec.Selector
	assert.Equal(t, 0, len(selector.MatchLabels))
	assert.ElementsMatch(t, selector.MatchExpressions, []metav1.LabelSelectorRequirement{
		{
			Key:      mlabels.ManagedByLabel,
			Operator: metav1.LabelSelectorOpIn,
			Values:   []string{mlabels.ManagedByLabelValue},
		},
		{
			Key:      mlabels.ReaperLabel,
			Operator: metav1.LabelSelectorOpIn,
			Values:   []string{reaper.Name},
		},
	})

	assert.Equal(t, labels, deployment.Spec.Template.Labels)

	podSpec := deployment.Spec.Template.Spec
	assert.Equal(t, 1, len(podSpec.Containers))

	container := podSpec.Containers[0]
	assert.Equal(t, image, container.Image)
	assert.ElementsMatch(t, container.Env, []corev1.EnvVar{
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
			Value: "[" + cassandraService + "]",
		},
	})

	probe := &corev1.Probe{
		Handler: corev1.Handler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: "/healthcheck",
				Port: intstr.FromInt(8081),
			},
		},
		InitialDelaySeconds: 10,
		PeriodSeconds:       3,
	}
	assert.Equal(t, probe, container.LivenessProbe)
	assert.Equal(t, probe, container.ReadinessProbe)
}