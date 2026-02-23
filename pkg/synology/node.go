package synology

import (
	"github.com/metal-stack/gardener-extension-csi-driver-synology/pkg/constants"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// GenerateNodeDaemonSet generates the CSI node DaemonSet
func GenerateNodeDaemonSet(namespace string) *appsv1.DaemonSet {
	hostPathDirectoryOrCreate := corev1.HostPathDirectoryOrCreate
	hostPathDirectory := corev1.HostPathDirectory
	bidirectional := corev1.MountPropagationBidirectional

	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      constants.NodeName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "synology-csi",
				"app.kubernetes.io/component": "node",
			},
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app.kubernetes.io/name":      "synology-csi",
					"app.kubernetes.io/component": "node",
				},
			},
			UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
				Type: appsv1.RollingUpdateDaemonSetStrategyType,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app.kubernetes.io/name":                 "synology-csi",
						"app.kubernetes.io/component":            "node",
						"networking.gardener.cloud/to-apiserver": "allowed",
						"networking.gardener.cloud/to-dns":       "allowed",
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: constants.NodeName,
					HostNetwork:        true,
					DNSPolicy:          corev1.DNSClusterFirstWithHostNet,
					PriorityClassName:  "system-node-critical",
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
									Value: "unix://csi/csi.sock",
								},
							},
							SecurityContext: &corev1.SecurityContext{
								Privileged:               new(true),
								AllowPrivilegeEscalation: new(true),
								Capabilities: &corev1.Capabilities{
									Add: []corev1.Capability{"SYS_ADMIN"},
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "plugin-dir",
									MountPath: "/csi",
								},
								{
									Name:             "pods-mount-dir",
									MountPath:        "/var/lib/kubelet",
									MountPropagation: &bidirectional,
								},
								{
									Name:      "device-dir",
									MountPath: "/dev",
								},
								{
									Name:      "host-root",
									MountPath: "/host",
								},
								{
									Name:      "client-info",
									MountPath: "/etc/synology",
									ReadOnly:  true,
								},
							},
						},
						// CSI Node Driver Registrar
						{
							Name:  "csi-node-driver-registrar",
							Image: constants.ImageCSINodeDriverRegistrar,
							Args: []string{
								"--csi-address=$(ADDRESS)",
								"--kubelet-registration-path=$(DRIVER_REG_SOCK_PATH)",
								"--v=5",
							},
							Env: []corev1.EnvVar{
								{
									Name:  "ADDRESS",
									Value: "/csi/csi.sock",
								},
								{
									Name:  "DRIVER_REG_SOCK_PATH",
									Value: "/var/lib/kubelet/plugins/csi.san.synology.com/csi.sock",
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "plugin-dir",
									MountPath: "/csi",
								},
								{
									Name:      "registration-dir",
									MountPath: "/registration",
								},
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{
											"/csi-node-driver-registrar",
											"--kubelet-registration-path=$(DRIVER_REG_SOCK_PATH)",
											"--mode=kubelet-registration-probe",
										},
									},
								},
								InitialDelaySeconds: 30,
								TimeoutSeconds:      5,
								PeriodSeconds:       10,
							},
						},
						// Liveness Probe
						{
							Name:  "liveness-probe",
							Image: constants.ImageCSILivenessProbe,
							Args: []string{
								"--csi-address=$(ADDRESS)",
								"--health-port=9809",
							},
							Env: []corev1.EnvVar{
								{
									Name:  "ADDRESS",
									Value: "/csi/csi.sock",
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "plugin-dir",
									MountPath: "/csi",
								},
							},
							Ports: []corev1.ContainerPort{
								{
									Name:          "healthz",
									ContainerPort: 9809,
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
							Name: "plugin-dir",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/var/lib/kubelet/plugins/csi.san.synology.com/",
									Type: &hostPathDirectoryOrCreate,
								},
							},
						},
						{
							Name: "registration-dir",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/var/lib/kubelet/plugins_registry/",
									Type: &hostPathDirectory,
								},
							},
						},
						{
							Name: "pods-mount-dir",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/var/lib/kubelet",
									Type: &hostPathDirectory,
								},
							},
						},
						{
							Name: "device-dir",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/dev",
									Type: &hostPathDirectory,
								},
							},
						},
						{
							Name: "host-root",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/",
									Type: &hostPathDirectory,
								},
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
