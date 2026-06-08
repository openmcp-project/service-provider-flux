package e2e

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	klientresources "sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	libutils "github.com/openmcp-project/openmcp-operator/lib/utils"
	"github.com/openmcp-project/service-provider-flux/api/v1alpha1"
	"github.com/openmcp-project/service-provider-flux/pkg/flux"

	"github.com/openmcp-project/openmcp-testing/pkg/clusterutils"
	openmcpconditions "github.com/openmcp-project/openmcp-testing/pkg/conditions"
	"github.com/openmcp-project/openmcp-testing/pkg/providers"
	"github.com/openmcp-project/openmcp-testing/pkg/resources"
)

const (
	mcpName               = "test-mcp"
	mcpCAConfigMapName    = "custom-ca-bundle"
	caConfigMapNameUpdate = "flux-ca-bundle-update"
	caConfigMapKey        = "ca.crt"
	caVolumeName          = "custom-ca-bundle"
	caMountPath           = "/etc/open-control-plane/custom-ca"
	sslCertDirEnvName     = "SSL_CERT_DIR"
	sslCertDirEnvValue    = "/etc/ssl/certs:/etc/pki/tls/certs:/etc/open-control-plane/custom-ca"
)

func TestServiceProvider(t *testing.T) {
	var onboardingList unstructured.UnstructuredList
	var mcpList unstructured.UnstructuredList
	basicProviderTest := features.New("provider test").
		Setup(func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			if _, err := resources.CreateObjectsFromDir(ctx, c, "platform"); err != nil {
				t.Errorf("failed to create platform cluster objects: %v", err)
			}
			return ctx
		}).
		Setup(providers.CreateMCP(mcpName)).
		Assess("verify service can be successfully consumed",
			func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
				onboardingConfig, err := clusterutils.OnboardingConfig()
				if err != nil {
					t.Error(err)
					return ctx
				}
				objList, err := resources.CreateObjectsFromDir(ctx, onboardingConfig, "onboarding")
				if err != nil {
					t.Errorf("failed to create onboarding cluster objects: %v", err)
					return ctx
				}
				for _, obj := range objList.Items {
					if err := wait.For(openmcpconditions.Match(&obj, onboardingConfig, "Ready", corev1.ConditionTrue)); err != nil {
						t.Error(err)
					}
				}
				objList.DeepCopyInto(&onboardingList)
				return ctx
			},
		).
		Assess("platform cluster resources are reconciled successfully", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			tenantNamespace, err := libutils.StableMCPNamespace(mcpName, "default")
			if err != nil {
				t.Errorf("failed to get tenant namespace: %v", err)
				return ctx
			}
			ociRepo := &sourcev1.OCIRepository{}
			ociRepo.SetName("flux")
			ociRepo.SetNamespace(tenantNamespace)
			if err := wait.For(openmcpconditions.Match(ociRepo, c, "Ready", corev1.ConditionTrue), wait.WithTimeout(2*time.Minute)); err != nil {
				t.Errorf("OCIRepository not ready: %v", err)
			}
			helmRelease := &helmv2.HelmRelease{}
			helmRelease.SetName("flux")
			helmRelease.SetNamespace(tenantNamespace)
			if err := wait.For(openmcpconditions.Match(helmRelease, c, "Ready", corev1.ConditionTrue), wait.WithTimeout(2*time.Minute)); err != nil {
				t.Errorf("HelmRelease not ready: %v", err)
			}
			chartSecret := &corev1.Secret{}
			chartSecret.SetName("sp-flux-flux-registry-credentials")
			chartSecret.SetNamespace(tenantNamespace)
			pullSecrets := &corev1.SecretList{
				Items: []corev1.Secret{*chartSecret},
			}
			if err := wait.For(conditions.New(c.Client().Resources()).ResourcesFound(pullSecrets), wait.WithTimeout(2*time.Minute)); err != nil {
				t.Errorf("pull secret not found: %v", err)
			}
			return ctx
		}).
		Assess("ManagedControlPlane resources have been created", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			mcp, err := clusterutils.MCPConfig(ctx, c, mcpName)
			if err != nil {
				t.Error(err)
				return ctx
			}
			imagePullSecret := &corev1.Secret{}
			imagePullSecret.SetName("flux-registry-credentials")
			imagePullSecret.SetNamespace("flux-system")
			list := &corev1.SecretList{
				Items: []corev1.Secret{*imagePullSecret},
			}
			if err := wait.For(conditions.New(mcp.Client().Resources()).ResourcesFound(list), wait.WithTimeout(2*time.Minute)); err != nil {
				t.Errorf("image pull secret not found on control plane: %v", err)
			}

			caBundleConfigMap := &corev1.ConfigMap{}
			caBundleConfigMap.SetName(mcpCAConfigMapName)
			caBundleConfigMap.SetNamespace("flux-system")
			cmList := &corev1.ConfigMapList{
				Items: []corev1.ConfigMap{*caBundleConfigMap},
			}
			if err := wait.For(conditions.New(mcp.Client().Resources()).ResourcesFound(cmList), wait.WithTimeout(2*time.Minute)); err != nil {
				t.Errorf("ca configmap not found on control plane: %v", err)
			}
			return ctx
		}).
		Assess("domain objects can be created", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			mcp, err := clusterutils.MCPConfig(ctx, c, mcpName)
			if err != nil {
				t.Error(err)
				return ctx
			}
			objList, err := resources.CreateObjectsFromDir(ctx, mcp, "mcp")
			if err != nil {
				t.Errorf("failed to create mcp cluster objects: %v", err)
				return ctx
			}
			if err := wait.For(conditions.New(mcp.Client().Resources()).ResourcesFound(objList)); err != nil {
				t.Error(err)
				return ctx
			}
			objList.DeepCopyInto(&mcpList)
			return ctx
		},
		).
		Assess("GitRepository becomes ready", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			mcp, err := clusterutils.MCPConfig(ctx, c, mcpName)
			if err != nil {
				t.Error(err)
				return ctx
			}
			// Wait for the GitRepository to have a Ready condition
			for _, obj := range mcpList.Items {
				if err := wait.For(openmcpconditions.Match(&obj, mcp, "Ready", corev1.ConditionTrue), wait.WithTimeout(2*time.Minute)); err != nil {
					t.Error(err)
				}
			}
			return ctx
		}).
		Assess("flux deployments mount custom ca and set SSL_CERT_DIR", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			mcp, err := clusterutils.MCPConfig(ctx, c, mcpName)
			if err != nil {
				t.Error(err)
				return ctx
			}

			deploymentNames := []string{
				"helm-controller",
				"image-automation-controller",
				"image-reflector-controller",
				"kustomize-controller",
				"notification-controller",
				"source-controller",
				"source-watcher",
			}

			for _, deploymentName := range deploymentNames {
				deployment := &appsv1.Deployment{}
				if err := mcp.Client().Resources().Get(ctx, deploymentName, "flux-system", deployment); err != nil {
					t.Errorf("failed to get deployment %s: %v", deploymentName, err)
					continue
				}

				hasCAVolume := false
				for _, volume := range deployment.Spec.Template.Spec.Volumes {
					if volume.Name == caVolumeName {
						hasCAVolume = true
						break
					}
				}
				if !hasCAVolume {
					t.Errorf("deployment %s does not have %s volume", deploymentName, caVolumeName)
					continue
				}

				hasCAMount := false
				hasSSLCertDirEnv := false
				for _, container := range deployment.Spec.Template.Spec.Containers {
					for _, volumeMount := range container.VolumeMounts {
						if volumeMount.Name == caVolumeName && volumeMount.MountPath == caMountPath {
							hasCAMount = true
							break
						}
					}
					for _, envVar := range container.Env {
						if envVar.Name == sslCertDirEnvName && envVar.Value == sslCertDirEnvValue {
							hasSSLCertDirEnv = true
							break
						}
					}
				}

				if !hasCAMount {
					t.Errorf("deployment %s does not mount %s at %s", deploymentName, caVolumeName, caMountPath)
				}
				if !hasSSLCertDirEnv {
					t.Errorf("deployment %s does not set %s=%s", deploymentName, sslCertDirEnvName, sslCertDirEnvValue)
				}
			}

			return ctx
		}).
		Assess("provider config update with new secret references", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			if err := v1alpha1.AddToScheme(c.Client().Resources().GetScheme()); err != nil {
				t.Errorf("failed to add api types to client scheme: %s", err)
				return ctx
			}
			providerConfig := &v1alpha1.ProviderConfig{}
			providerConfig.SetName("flux")

			if err := c.Client().Resources().Get(ctx, "flux", "openmcp-system", providerConfig); err != nil {
				t.Errorf("failed to get provider config: %v", err)
				return ctx
			}
			providerConfig.Spec.Versions[0].ChartPullSecret = "flux-registry-credentials-update"
			values := flux.HelmValues{
				ImagePullSecrets: []corev1.LocalObjectReference{
					{Name: "flux-registry-credentials-update"},
				},
			}
			bytes, err := json.Marshal(values)
			if err != nil {
				t.Errorf("failed to marshal helm values: %v", err)
				return ctx
			}
			providerConfig.Spec.Versions[0].Values = &v1.JSON{Raw: bytes}
			if err := c.Client().Resources().Update(ctx, providerConfig); err != nil {
				t.Errorf("failed to update provider config: %v", err)
			}
			// verify service stays healthy
			onboardingConfig, err := clusterutils.OnboardingConfig()
			v1alpha1.AddToScheme(onboardingConfig.GetClient().Resources().GetScheme())
			if err != nil {
				t.Error(err)
				return ctx
			}
			flux := &v1alpha1.Flux{}
			flux.SetName(mcpName)
			flux.SetNamespace(corev1.NamespaceDefault)
			if err := wait.For(openmcpconditions.Match(flux, onboardingConfig, "Ready", corev1.ConditionTrue), wait.WithTimeout(2*time.Minute)); err != nil {
				t.Errorf("Flux not ready after provider config update: %v", err)
			}
			return ctx
		}).
		Assess("platform chart pull secret updated", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			tenantNamespace, err := libutils.StableMCPNamespace(mcpName, "default")
			if err != nil {
				t.Errorf("failed to get tenant namespace: %v", err)
				return ctx
			}
			chartSecret := &corev1.Secret{}
			chartSecret.SetName("sp-flux-flux-registry-credentials")
			chartSecret.SetNamespace(tenantNamespace)
			if err := wait.For(conditions.New(c.Client().Resources()).ResourceDeleted(chartSecret), wait.WithTimeout(2*time.Minute)); err != nil {
				t.Errorf("orphaned chart pull secret is not deleted: %v", err)
			}
			chartSecret.SetName("sp-flux-flux-registry-credentials-update")
			if err := wait.For(conditions.New(c.Client().Resources()).ResourcesFound(&corev1.SecretList{
				Items: []corev1.Secret{*chartSecret},
			}), wait.WithTimeout(2*time.Minute)); err != nil {
				t.Errorf("pull secret not found: %v", err)
			}
			return ctx
		}).
		Assess("control plane image pull secrets updated", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			mcp, err := clusterutils.MCPConfig(ctx, c, mcpName)
			if err != nil {
				t.Error(err)
				return ctx
			}
			imagePullSecret := &corev1.Secret{}
			imagePullSecret.SetName("flux-registry-credentials")
			imagePullSecret.SetNamespace("flux-system")
			if err := wait.For(conditions.New(c.Client().Resources()).ResourceDeleted(imagePullSecret), wait.WithTimeout(2*time.Minute)); err != nil {
				t.Errorf("orphaned image pull secret is not deleted: %v", err)
			}

			imagePullSecret.SetName("flux-registry-credentials-update")
			list := &corev1.SecretList{
				Items: []corev1.Secret{*imagePullSecret},
			}
			if err := wait.For(conditions.New(mcp.Client().Resources()).ResourcesFound(list), wait.WithTimeout(2*time.Minute)); err != nil {
				t.Errorf("image pull secret not found on control plane: %v", err)
			}
			return ctx
		}).
		Assess("provider config update with new ca bundle reference", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			if err := v1alpha1.AddToScheme(c.Client().Resources().GetScheme()); err != nil {
				t.Errorf("failed to add api types to client scheme: %s", err)
				return ctx
			}
			providerConfig := &v1alpha1.ProviderConfig{}
			providerConfig.SetName("flux")
			if err := c.Client().Resources().Get(ctx, "flux", "openmcp-system", providerConfig); err != nil {
				t.Errorf("failed to get provider config: %v", err)
				return ctx
			}
			providerConfig.Spec.CaBundleRef = &corev1.ConfigMapKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: caConfigMapNameUpdate},
				Key:                  caConfigMapKey,
			}
			if err := c.Client().Resources().Update(ctx, providerConfig); err != nil {
				t.Errorf("failed to update provider config: %v", err)
				return ctx
			}

			onboardingConfig, err := clusterutils.OnboardingConfig()
			v1alpha1.AddToScheme(onboardingConfig.GetClient().Resources().GetScheme())
			if err != nil {
				t.Error(err)
				return ctx
			}

			fluxObj := &v1alpha1.Flux{}
			fluxObj.SetName(mcpName)
			fluxObj.SetNamespace(corev1.NamespaceDefault)
			if err := wait.For(openmcpconditions.Match(fluxObj, onboardingConfig, "Ready", corev1.ConditionTrue), wait.WithTimeout(2*time.Minute)); err != nil {
				t.Errorf("Flux not ready after provider config update: %v", err)
			}
			return ctx
		}).
		Assess("control plane ca configmap updated", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			mcp, err := clusterutils.MCPConfig(ctx, c, mcpName)
			if err != nil {
				t.Error(err)
				return ctx
			}

			mcpCaConfigMap := &corev1.ConfigMap{}
			mcpCaConfigMap.SetName(mcpCAConfigMapName)
			mcpCaConfigMap.SetNamespace("flux-system")
			list := &corev1.ConfigMapList{
				Items: []corev1.ConfigMap{*mcpCaConfigMap},
			}
			if err := wait.For(conditions.New(mcp.Client().Resources()).ResourcesFound(list), wait.WithTimeout(2*time.Minute)); err != nil {
				t.Errorf("ca configmap not found on control plane: %v", err)
			}
			return ctx
		}).
		Assess("provider config update drops pull secrets", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			if err := v1alpha1.AddToScheme(c.Client().Resources().GetScheme()); err != nil {
				t.Errorf("failed to add api types to client scheme: %s", err)
				return ctx
			}
			providerConfig := &v1alpha1.ProviderConfig{}
			providerConfig.SetName("flux")
			if err := c.Client().Resources().Get(ctx, "flux", "openmcp-system", providerConfig); err != nil {
				t.Errorf("failed to get provider config: %v", err)
				return ctx
			}
			providerConfig.Spec.Versions[0].ChartPullSecret = ""
			providerConfig.Spec.Versions[0].Values = nil
			providerConfig.Spec.CaBundleRef = nil
			if err := c.Client().Resources().Update(ctx, providerConfig); err != nil {
				t.Errorf("failed to update provider config: %v", err)
			}
			// verify service stays healthy
			onboardingConfig, err := clusterutils.OnboardingConfig()
			v1alpha1.AddToScheme(onboardingConfig.GetClient().Resources().GetScheme())
			if err != nil {
				t.Error(err)
				return ctx
			}
			flux := &v1alpha1.Flux{}
			flux.SetName(mcpName)
			flux.SetNamespace(corev1.NamespaceDefault)
			if err := wait.For(openmcpconditions.Match(flux, onboardingConfig, "Ready", corev1.ConditionTrue), wait.WithTimeout(2*time.Minute)); err != nil {
				t.Errorf("Flux not ready after provider config update: %v", err)
			}

			return ctx
		}).
		Assess("platform chart pull secret deleted", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			tenantNamespace, err := libutils.StableMCPNamespace(mcpName, "default")
			if err != nil {
				t.Errorf("failed to get tenant namespace: %v", err)
				return ctx
			}
			spFluxSecrets := &corev1.SecretList{}
			if err := wait.For(conditions.New(c.Client().Resources().WithNamespace(tenantNamespace)).
				ResourceListN(spFluxSecrets, 0, klientresources.WithLabelSelector(
					labels.FormatLabels(map[string]string{flux.LabelManagedBy: "service-provider-flux"}))),
				wait.WithTimeout(2*time.Minute)); err != nil {
				t.Errorf("orphaned chart pull secret is not deleted: %v", err)
			}
			return ctx
		}).
		Assess("control plane image pull secrets deleted", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			mcp, err := clusterutils.MCPConfig(ctx, c, mcpName)
			if err != nil {
				t.Error(err)
				return ctx
			}
			spFluxSecrets := &corev1.SecretList{}
			if err := wait.For(conditions.New(mcp.Client().Resources().WithNamespace("flux-system")).
				ResourceListN(spFluxSecrets, 0, klientresources.WithLabelSelector(
					labels.FormatLabels(map[string]string{flux.LabelManagedBy: "service-provider-flux"}))),
				wait.WithTimeout(2*time.Minute)); err != nil {
				t.Errorf("orphaned image pull secret is not deleted: %v", err)
			}
			return ctx
		}).
		Assess("control plane ca configmap deleted", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			mcp, err := clusterutils.MCPConfig(ctx, c, mcpName)
			if err != nil {
				t.Error(err)
				return ctx
			}
			caConfigMap := &corev1.ConfigMapList{}
			if err := wait.For(conditions.New(mcp.Client().Resources().WithNamespace("flux-system")).
				ResourceListN(caConfigMap, 0, klientresources.WithLabelSelector(
					labels.FormatLabels(map[string]string{flux.LabelManagedBy: "service-provider-flux"}))),
				wait.WithTimeout(2*time.Minute)); err != nil {
				t.Errorf("orphaned ca configmap is not deleted: %v", err)
			}
			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			mcp, err := clusterutils.MCPConfig(ctx, c, mcpName)
			if err != nil {
				t.Error(err)
				return ctx
			}
			for _, obj := range mcpList.Items {
				if err := resources.DeleteObject(ctx, mcp, &obj, wait.WithTimeout(time.Minute)); err != nil {
					t.Errorf("failed to delete mcp object: %v", err)
				}
			}
			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			onboardingConfig, err := clusterutils.OnboardingConfig()
			if err != nil {
				t.Error(err)
				return ctx
			}
			for _, obj := range onboardingList.Items {
				if err := resources.DeleteObject(ctx, onboardingConfig, &obj, wait.WithTimeout(time.Minute)); err != nil {
					t.Errorf("failed to delete onboarding object: %v", err)
				}
			}
			return ctx
		}).
		Teardown(providers.DeleteMCP(mcpName, wait.WithTimeout(5*time.Minute)))
	testenv.Test(t, basicProviderTest.Feature())
}
