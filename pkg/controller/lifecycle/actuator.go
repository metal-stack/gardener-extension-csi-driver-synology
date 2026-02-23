package lifecycle

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	extensionsconfigv1alpha1 "github.com/gardener/gardener/extensions/pkg/apis/config/v1alpha1"
	"github.com/gardener/gardener/extensions/pkg/controller"
	"github.com/gardener/gardener/extensions/pkg/controller/extension"
	gutil "github.com/gardener/gardener/extensions/pkg/util"
	"github.com/gardener/gardener/pkg/apis/core/v1beta1/helper"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"github.com/gardener/gardener/pkg/client/kubernetes"
	"github.com/gardener/gardener/pkg/extensions"
	"github.com/gardener/gardener/pkg/utils/managedresources"
	"github.com/go-logr/logr"
	"github.com/metal-stack/gardener-extension-csi-driver-synology/pkg/apis/config"
	"github.com/metal-stack/gardener-extension-csi-driver-synology/pkg/apis/csidriversynology/v1alpha1"
	"github.com/metal-stack/gardener-extension-csi-driver-synology/pkg/constants"
	"github.com/metal-stack/gardener-extension-csi-driver-synology/pkg/synology"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Actuator acts upon Extension resources
type Actuator struct {
	client  client.Client
	decoder runtime.Decoder
	config  config.ControllerConfiguration
}

// NewActuator creates a new Actuator
func NewActuator(client client.Client, config config.ControllerConfiguration) extension.Actuator {
	return &Actuator{
		client:  client,
		decoder: serializer.NewCodecFactory(client.Scheme(), serializer.EnableStrict).UniversalDecoder(),
		config:  config,
	}
}

// Reconcile the Extension resource
func (a *Actuator) Reconcile(ctx context.Context, log logr.Logger, ex *extensionsv1alpha1.Extension) error {
	shootConfig := &v1alpha1.CsiDriverSynologyConfig{}
	if ex.Spec.ProviderConfig != nil {
		_, _, err := a.decoder.Decode(ex.Spec.ProviderConfig.Raw, nil, shootConfig)
		if err != nil {
			return fmt.Errorf("failed to decode provider config: %w", err)
		}
	}

	namespace := ex.GetNamespace()
	shootName := namespace
	shootNamespace := namespace

	log.Info("Reconciling Synology CSI extension", "namespace", namespace)

	cluster, err := controller.GetCluster(ctx, a.client, namespace)
	if err != nil {
		return err
	}

	secret, err := a.getAdminSynologySecret(ctx, cluster, a.config.SynologyConfig.SecretRef)
	if err != nil {
		return err
	}

	adminUsername, adminPassword, err := extractAdminSynologySecret(secret)
	if err != nil {
		return err
	}

	// Create Synology client
	synologyClient, err := synology.NewClient(
		a.config.SynologyConfig.URL,
		adminUsername,
		adminPassword,
	)

	if err != nil {
		return fmt.Errorf("failed to create Synology client: %w", err)
	}

	if err := synologyClient.Login(); err != nil {
		return fmt.Errorf("failed to login to Synology NAS: %w", err)
	}
	defer func() {
		_ = synologyClient.Logout()
	}()

	shootUsername := synology.GenerateShootUsername(shootName, shootNamespace)
	shootPassword := ""

	user, err := synologyClient.GetUser(shootUsername)
	if err != nil {
		return fmt.Errorf("failed to get user from Synology: %w", err)
	}

	if user == nil {
		shootPassword, err = synology.GenerateRandomPassword(16)
		if err != nil {
			return fmt.Errorf("failed to generate password: %w", err)
		}

		if err := synologyClient.CreateUser(shootUsername, shootPassword); err != nil {
			return fmt.Errorf("failed to create user on Synology: %w", err)
		}
	} else {
		secret, err := a.getShootSynologySecret(ctx, ex.Namespace)
		if err != nil {
			return err
		}

		_, shootPwd, err := extractShootSynologySecret(secret)
		if err != nil {
			return err
		}

		shootPassword = shootPwd
	}

	u, err := url.Parse(a.config.SynologyConfig.URL)
	if err != nil {
		return fmt.Errorf("failed to parse synology-url: %w", err)
	}

	port, err := strconv.Atoi(u.Port())
	if err != nil {
		return fmt.Errorf("failed to parse synology-url port: %w", err)
	}

	// Create manifest config
	manifestConfig := &synology.ManifestConfig{
		Namespace: constants.ShootTargetNamespace,
		Url:       a.config.SynologyConfig.URL,
		Username:  shootUsername,
		Password:  shootPassword,
		Clients: []synology.ClientConfig{
			{
				Host:     u.Hostname(),
				Port:     port,
				HTTPS:    u.Scheme == "https",
				Username: shootUsername,
				Password: shootPassword,
			},
			{
				Host:     u.Hostname(),
				Port:     5001,
				HTTPS:    u.Scheme == "https",
				Username: shootUsername,
				Password: shootPassword,
			},
		},
	}

	objects, err := a.generateManifests(manifestConfig)
	if err != nil {
		return fmt.Errorf("unable to generate resource manifests for shoot: %w", err)
	}

	shootResources, err := managedresources.NewRegistry(kubernetes.ShootScheme, kubernetes.ShootCodec, kubernetes.ShootSerializer).AddAllAndSerialize(objects...)
	if err != nil {
		return fmt.Errorf("unable to create registry: %w", err)
	}

	err = managedresources.CreateForShoot(ctx, a.client, ex.Namespace, constants.CSIDriverName, constants.ExtensionType, false, shootResources)
	if err != nil {
		return fmt.Errorf("unable to create shoot resources: %w", err)
	}

	log.Info("Successfully reconciled Synology CSI extension")
	return nil
}

// Delete the Extension resource
func (a *Actuator) Delete(ctx context.Context, log logr.Logger, ex *extensionsv1alpha1.Extension) error {
	return nil
}

// Restore the Extension resource
func (a *Actuator) Restore(ctx context.Context, log logr.Logger, ex *extensionsv1alpha1.Extension) error {
	return a.Reconcile(ctx, log, ex)
}

// Migrate the Extension resource
func (a *Actuator) Migrate(ctx context.Context, log logr.Logger, ex *extensionsv1alpha1.Extension) error {
	return nil
}

// ForceDelete forcefully deletes the Extension resource
func (a *Actuator) ForceDelete(ctx context.Context, log logr.Logger, ex *extensionsv1alpha1.Extension) error {
	return nil
}

// generateManifests deploys all necessary resources to the shoot cluster
func (a *Actuator) generateManifests(config *synology.ManifestConfig) ([]client.Object, error) {
	secret, err := synology.GenerateSecret(config)
	if err != nil {
		return nil, fmt.Errorf("failed to generate secret: %w", err)
	}

	objects := []client.Object{
		synology.GenerateServiceAccount(config.Namespace, constants.ControllerName),
		synology.GenerateServiceAccount(config.Namespace, constants.NodeName),
		synology.GenerateControllerClusterRole(),
		synology.GenerateNodeClusterRole(),
		synology.GenerateClusterRoleBinding(constants.ControllerName, config.Namespace, constants.ControllerName),
		synology.GenerateClusterRoleBinding(constants.NodeName, config.Namespace, constants.NodeName),
		secret,
		synology.GenerateCSIDriver(),
		synology.GenerateService(config.Namespace),
		synology.GenerateControllerDeployment(config.Namespace),
		synology.GenerateNodeDaemonSet(config.Namespace),
		synology.GenerateStorageClass(config.Namespace),
		synology.GenerateAllowAllEgressNetworkPolicy(config.Namespace),
	}

	return objects, nil
}

func (a *Actuator) getAdminSynologySecret(ctx context.Context, cluster *extensions.Cluster, secretName string) (*corev1.Secret, error) {
	fromShootResources := func() (*corev1.Secret, error) {
		secretRef := helper.GetResourceByName(cluster.Shoot.Spec.Resources, secretName)
		if secretRef == nil {
			return nil, nil
		}

		secret := &corev1.Secret{}
		err := controller.GetObjectByReference(ctx, a.client, &secretRef.ResourceRef, cluster.ObjectMeta.Name, secret)
		if err != nil {
			return nil, fmt.Errorf("unable to get referenced secret: %w", err)
		}

		return secret, nil
	}

	secret, err := fromShootResources()
	if err != nil {
		return nil, err
	}

	if secret == nil {
		return nil, fmt.Errorf("no admin synology secret found %q", secretName)
	}

	return secret, nil
}

func (a *Actuator) getShootSynologySecret(ctx context.Context, namespace string) (*corev1.Secret, error) {
	_, shootClient, err := gutil.NewClientForShoot(ctx, a.client, namespace, client.Options{}, extensionsconfigv1alpha1.RESTOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create shoot client: %w", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      constants.SecretName,
			Namespace: constants.ShootTargetNamespace,
		},
	}

	err = shootClient.Get(ctx, client.ObjectKeyFromObject(secret), secret)
	if err != nil {
		return nil, fmt.Errorf("unable to get synology-credentials: %w", err)
	}

	return secret, nil
}

func extractAdminSynologySecret(secret *corev1.Secret) (admin string, password string, err error) {
	userBytes, ok := secret.Data[constants.SynologySecretAdminUserRef]
	if !ok {
		return "", "", fmt.Errorf(
			"referenced synology secret does not contain %q",
			constants.SynologySecretAdminUserRef,
		)
	}

	passwordBytes, ok := secret.Data[constants.SynologySecretAdminPasswordRef]
	if !ok {
		return "", "", fmt.Errorf(
			"referenced synology secret does not contain %q",
			constants.SynologySecretAdminPasswordRef,
		)
	}

	return string(userBytes), string(passwordBytes), nil
}

func extractShootSynologySecret(secret *corev1.Secret) (admin string, password string, err error) {
	userBytes, ok := secret.Data[constants.SynologySecretShootUserRef]
	if !ok {
		return "", "", fmt.Errorf(
			"referenced synology secret does not contain %q",
			constants.SynologySecretShootUserRef,
		)
	}

	passwordBytes, ok := secret.Data[constants.SynologySecretShootPasswordRef]
	if !ok {
		return "", "", fmt.Errorf(
			"referenced synology secret does not contain %q",
			constants.SynologySecretShootPasswordRef,
		)
	}

	return string(userBytes), string(passwordBytes), nil
}
