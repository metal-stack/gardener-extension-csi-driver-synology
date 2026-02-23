package synology

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/metal-stack/gardener-extension-csi-driver-synology/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// ClientConfig resembles the Helm chart's client-info.yaml schema.
type ClientConfig struct {
	Host     string
	Port     int
	HTTPS    bool
	Username string
	Password string
}

// ManifestConfig contains configuration for generating manifests
type ManifestConfig struct {
	Namespace string

	// Backward-compatible single-client inputs (will be mapped into Clients if Clients is empty).
	Url      string
	Username string
	Password string

	// Helm-like multi-client config (preferred).
	Clients []ClientConfig
}

// GenerateNamespace generates the namespace for the CSI driver
func GenerateNamespace(namespace string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name": "synology-csi",
			},
		},
	}
}

// GenerateServiceAccount generates the service account
func GenerateServiceAccount(namespace, name string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "synology-csi",
				"app.kubernetes.io/component": name,
			},
		},
	}
}

// GenerateControllerClusterRole generates the cluster role for the controller
func GenerateControllerClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: constants.ControllerName,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "synology-csi",
				"app.kubernetes.io/component": "controller",
			},
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"persistentvolumes"},
				Verbs:     []string{"get", "list", "watch", "create", "delete", "patch", "update"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"persistentvolumeclaims"},
				Verbs:     []string{"get", "list", "watch", "update"},
			},
			{
				APIGroups: []string{"storage.k8s.io"},
				Resources: []string{"storageclasses"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"events"},
				Verbs:     []string{"list", "watch", "create", "update", "patch"},
			},
			{
				APIGroups: []string{"storage.k8s.io"},
				Resources: []string{"csinodes"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"nodes"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"secrets"},
				Verbs:     []string{"get", "list"},
			},
			{
				APIGroups: []string{"storage.k8s.io"},
				Resources: []string{"volumeattachments"},
				Verbs:     []string{"get", "list", "watch", "update", "patch"},
			},
			{
				APIGroups: []string{"storage.k8s.io"},
				Resources: []string{"volumeattachments/status"},
				Verbs:     []string{"patch"},
			},
			{
				APIGroups: []string{"snapshot.storage.k8s.io"},
				Resources: []string{"volumesnapshots"},
				Verbs:     []string{"get", "list"},
			},
			{
				APIGroups: []string{"snapshot.storage.k8s.io"},
				Resources: []string{"volumesnapshotcontents"},
				Verbs:     []string{"get", "list", "watch", "update", "patch", "create", "delete"},
			},
			{
				APIGroups: []string{"snapshot.storage.k8s.io"},
				Resources: []string{"volumesnapshotclasses"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{"snapshot.storage.k8s.io"},
				Resources: []string{"volumesnapshotcontents/status"},
				Verbs:     []string{"update", "patch"},
			},
		},
	}
}

// GenerateNodeClusterRole generates the cluster role for the node
func GenerateNodeClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: constants.NodeName,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "synology-csi",
				"app.kubernetes.io/component": "node",
			},
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"secrets"},
				Verbs:     []string{"get", "list"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"nodes"},
				Verbs:     []string{"get", "list", "update"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"namespaces"},
				Verbs:     []string{"get", "list"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"persistentvolumes"},
				Verbs:     []string{"get", "list", "watch", "update"},
			},
			{
				APIGroups: []string{"storage.k8s.io"},
				Resources: []string{"volumeattachments"},
				Verbs:     []string{"get", "list", "watch", "update"},
			},
		},
	}
}

// GenerateClusterRoleBinding generates the cluster role binding
func GenerateClusterRoleBinding(name, namespace, serviceAccount string) *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "synology-csi",
				"app.kubernetes.io/component": name,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     name,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      serviceAccount,
				Namespace: namespace,
			},
		},
	}
}

// buildClientInfoYAML renders the Helm-like client-info.yaml content.
func buildClientInfoYAML(clients []ClientConfig) (string, error) {
	if len(clients) == 0 {
		return "", fmt.Errorf("no clients configured")
	}

	// stable output (helpful for diffs/tests)
	sorted := make([]ClientConfig, 0, len(clients))
	sorted = append(sorted, clients...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Host != sorted[j].Host {
			return sorted[i].Host < sorted[j].Host
		}
		if sorted[i].Port != sorted[j].Port {
			return sorted[i].Port < sorted[j].Port
		}
		// false before true
		return !sorted[i].HTTPS && sorted[j].HTTPS
	})

	var b strings.Builder
	b.WriteString("clients:\n")
	for _, c := range sorted {
		if c.Host == "" {
			return "", fmt.Errorf("client host must not be empty")
		}
		if c.Port <= 0 || c.Port > 65535 {
			return "", fmt.Errorf("invalid client port %d for host %q", c.Port, c.Host)
		}
		if c.Username == "" {
			return "", fmt.Errorf("client username must not be empty for host %q", c.Host)
		}
		if c.Password == "" {
			return "", fmt.Errorf("client password must not be empty for host %q", c.Host)
		}

		// Matches the helm chart example ordering/shape.
		b.WriteString("- host: " + c.Host + "\n")
		b.WriteString("  https: " + strconv.FormatBool(c.HTTPS) + "\n")
		b.WriteString("  password: " + c.Password + "\n")
		b.WriteString("  port: " + strconv.Itoa(c.Port) + "\n")
		b.WriteString("  username: " + c.Username + "\n")
	}
	return b.String(), nil
}

func normalizeClients(config *ManifestConfig) ([]ClientConfig, error) {
	// Preferred: explicit multi-client config
	if len(config.Clients) > 0 {
		clients := make([]ClientConfig, 0, len(config.Clients))
		for _, c := range config.Clients {
			// allow Username/Password defaults from top-level (handy for same creds on all clients)
			if c.Username == "" {
				c.Username = config.Username
			}
			if c.Password == "" {
				c.Password = config.Password
			}
			clients = append(clients, c)
		}
		return clients, nil
	}

	// Backward-compatible: single URL + creds -> one clients[] entry
	if config.Url == "" {
		return nil, fmt.Errorf("either Clients or Url must be set")
	}

	u, err := url.Parse(config.Url)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Synology URL: %w", err)
	}

	portStr := u.Port()
	if portStr == "" {
		return nil, fmt.Errorf("synology url must include an explicit port (got %q)", config.Url)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("invalid port in synology url %q: %w", config.Url, err)
	}

	https := (u.Scheme == "https")

	return []ClientConfig{
		{
			Host:     u.Hostname(),
			Port:     port,
			HTTPS:    https,
			Username: config.Username,
			Password: config.Password,
		},
	}, nil
}

// GenerateSecret generates the secret containing Synology client-info.yaml (Helm chart flow)
func GenerateSecret(config *ManifestConfig) (*corev1.Secret, error) {
	clients, err := normalizeClients(config)
	if err != nil {
		return nil, err
	}

	clientInfoYAML, err := buildClientInfoYAML(clients)
	if err != nil {
		return nil, err
	}

	// Helm chart uses stringData with a single key: client-info.yaml
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      constants.SecretName,
			Namespace: config.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name": "synology-csi",
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"client-info.yaml": clientInfoYAML,
			"user":             config.Username,
			"password":         config.Password,
		},
	}
	return secret, nil
}

// GenerateCSIDriver generates the CSIDriver resource
func GenerateCSIDriver() *storagev1.CSIDriver {
	return &storagev1.CSIDriver{
		ObjectMeta: metav1.ObjectMeta{
			Name: constants.CSIDriverName,
			Labels: map[string]string{
				"app.kubernetes.io/name": "synology-csi",
			},
		},
		Spec: storagev1.CSIDriverSpec{
			AttachRequired: new(true),
			PodInfoOnMount: new(false),
			VolumeLifecycleModes: []storagev1.VolumeLifecycleMode{
				storagev1.VolumeLifecyclePersistent,
			},
		},
	}
}

// GenerateStorageClass generates the default StorageClass
func GenerateStorageClass(namespace string) *storagev1.StorageClass {
	reclaimPolicy := corev1.PersistentVolumeReclaimDelete
	volumeBindingMode := storagev1.VolumeBindingImmediate
	allowVolumeExpansion := true

	return &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "synology-iscsi",
			Labels: map[string]string{
				"app.kubernetes.io/name": "synology-csi",
			},
			Annotations: map[string]string{
				"storageclass.kubernetes.io/is-default-class": "true",
			},
		},
		Provisioner:          constants.CSIDriverName,
		ReclaimPolicy:        &reclaimPolicy,
		VolumeBindingMode:    &volumeBindingMode,
		AllowVolumeExpansion: &allowVolumeExpansion,
		Parameters: map[string]string{
			"protocol":         "iscsi",
			"fsType":           "ext4",
			"formatOptions":    "--no-discard",
			"mountPermissions": "0750",
			"location":         "/volume1",
			"dsm":              "172.18.0.2",
		},
	}
}

// GenerateService generates a service for the CSI controller
func GenerateService(namespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      constants.ControllerName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "synology-csi",
				"app.kubernetes.io/component": "controller",
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"app.kubernetes.io/name":      "synology-csi",
				"app.kubernetes.io/component": "controller",
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "healthz",
					Protocol:   corev1.ProtocolTCP,
					Port:       9808,
					TargetPort: intstr.FromString("healthz"),
				},
			},
		},
	}
}

// GenerateEgressNetworkPolicyToDSM allows egress from synology-csi pods.
func GenerateAllowAllEgressNetworkPolicy(namespace string) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "allow-all-egress-synology-csi",
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name": "synology-csi",
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app.kubernetes.io/name": "synology-csi",
				},
			},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeEgress,
			},
			Egress: []networkingv1.NetworkPolicyEgressRule{
				{},
			},
		},
	}
}
