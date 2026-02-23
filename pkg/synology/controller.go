package synology

import (
	"github.com/metal-stack/gardener-extension-csi-driver-synology/pkg/constants"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// GenerateControllerDeployment generates the CSI controller deployment
func GenerateControllerDeployment(namespace string) *appsv1.Deployment {
	replicas := int32(1)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      constants.ControllerName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "synology-csi",
				"app.kubernetes.io/component": "controller",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app.kubernetes.io/name":      "synology-csi",
					"app.kubernetes.io/component": "controller",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app.kubernetes.io/name":      "synology-csi",
						"app.kubernetes.io/component": "controller",
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: constants.ControllerName,
					PriorityClassName:  "system-cluster-critical",
					Containers: []corev1.Container{
						// CSI Driver Container
						{
							Name:  "synology-csi-driver",
							Image: constants.ImageCSIDriver,
							Args: []string{
								"--nodeid=$(NODE_ID)",
								"--endpoint=$(CSI_ENDPOINT)",
								"--client-info=/etc/synology/client-info.yaml",
								"--log-level=info",
							},
							Env: []corev1.EnvVar{
								{
									Name: "NODE_ID",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "spec.nodeName",
										},
									},
								},
								{
									Name:  "CSI_ENDPOINT",
									Value: "unix:///var/lib/csi/sockets/pluginproxy/csi.sock",
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "socket-dir",
									MountPath: "/var/lib/csi/sockets/pluginproxy/",
								},
								{
									Name:      "client-info",
									MountPath: "/etc/synology",
									ReadOnly:  true,
								},
							},
							SecurityContext: &corev1.SecurityContext{
								Privileged:               new(true),
								AllowPrivilegeEscalation: new(true),
								Capabilities: &corev1.Capabilities{
									Add: []corev1.Capability{"SYS_ADMIN"},
								},
							},
						},
						// CSI Provisioner
						{
							Name:  "csi-provisioner",
							Image: constants.ImageCSIProvisioner,
							Args: []string{
								"--csi-address=$(ADDRESS)",
								"--timeout=60s",
								"--v=5",
							},
							Env: []corev1.EnvVar{
								{
									Name:  "ADDRESS",
									Value: "/var/lib/csi/sockets/pluginproxy/csi.sock",
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "socket-dir",
									MountPath: "/var/lib/csi/sockets/pluginproxy/",
								},
							},
						},
						// CSI Attacher
						{
							Name:  "csi-attacher",
							Image: constants.ImageCSIAttacher,
							Args: []string{
								"--csi-address=$(ADDRESS)",
								"--timeout=60s",
								"--v=5",
							},
							Env: []corev1.EnvVar{
								{
									Name:  "ADDRESS",
									Value: "/var/lib/csi/sockets/pluginproxy/csi.sock",
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "socket-dir",
									MountPath: "/var/lib/csi/sockets/pluginproxy/",
								},
							},
						},
						// CSI Resizer
						{
							Name:  "csi-resizer",
							Image: constants.ImageCSIResizer,
							Args: []string{
								"--csi-address=$(ADDRESS)",
								"--timeout=60s",
								"--v=5",
								"--handle-volume-inuse-error=false",
							},
							Env: []corev1.EnvVar{
								{
									Name:  "ADDRESS",
									Value: "/var/lib/csi/sockets/pluginproxy/csi.sock",
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "socket-dir",
									MountPath: "/var/lib/csi/sockets/pluginproxy/",
								},
							},
						},
						// CSI Snapshotter
						{
							Name:  "csi-snapshotter",
							Image: constants.ImageCSISnapshotter,
							Args: []string{
								"--csi-address=$(ADDRESS)",
								"--timeout=60s",
								"--v=5",
							},
							Env: []corev1.EnvVar{
								{
									Name:  "ADDRESS",
									Value: "/var/lib/csi/sockets/pluginproxy/csi.sock",
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "socket-dir",
									MountPath: "/var/lib/csi/sockets/pluginproxy/",
								},
							},
						},
						// Liveness Probe
						{
							Name:  "liveness-probe",
							Image: constants.ImageCSILivenessProbe,
							Args: []string{
								"--csi-address=$(ADDRESS)",
								"--health-port=9808",
							},
							Env: []corev1.EnvVar{
								{
									Name:  "ADDRESS",
									Value: "/var/lib/csi/sockets/pluginproxy/csi.sock",
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "socket-dir",
									MountPath: "/var/lib/csi/sockets/pluginproxy/",
								},
							},
							Ports: []corev1.ContainerPort{
								{
									Name:          "healthz",
									ContainerPort: 9808,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromString("healthz"),
									},
								},
								InitialDelaySeconds: 10,
								TimeoutSeconds:      3,
								PeriodSeconds:       10,
								FailureThreshold:    5,
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "socket-dir",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
						{
							Name: "client-info",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: constants.SecretName,
									Items: []corev1.KeyToPath{
										{
											Key:  "client-info.yaml",
											Path: "client-info.yaml",
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
