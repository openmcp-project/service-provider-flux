package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"github.com/openmcp-project/openmcp-testing/pkg/clusterutils"
	openmcpconditions "github.com/openmcp-project/openmcp-testing/pkg/conditions"
	"github.com/openmcp-project/openmcp-testing/pkg/providers"
	"github.com/openmcp-project/openmcp-testing/pkg/resources"
)

const mcpName = "test-mcp"

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
		Assess("Platform Cluster: chart pull secret synced to tenant namespace",
			chartSecretSynced("sp-flux-flux-registry-credentials")).
		Assess("ManagedControlPlane: image pull secret synced to flux-system namespace",
			imagePullSecretSynced(mcpName, client.ObjectKey{Name: "flux-registry-credentials", Namespace: "flux-system"})).
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

// imagePullSecretSynced verifies that the given secret exists on the ManagedControlPlane.
func imagePullSecretSynced(mcpName string, secret client.ObjectKey) features.Func {
	return func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		mcp, err := clusterutils.MCPConfig(ctx, c, mcpName)
		if err != nil {
			t.Error(err)
			return ctx
		}
		secList := &corev1.SecretList{
			Items: []corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      secret.Name,
						Namespace: secret.Namespace,
					},
				},
			},
		}
		if err := wait.For(conditions.New(mcp.Client().Resources()).ResourcesFound(secList), wait.WithTimeout(2*time.Minute)); err != nil {
			t.Errorf("image pull secret %s/%s not found on MCP %s: %v", secret.Namespace, secret.Name, mcpName, err)
		}
		return ctx
	}
}

// chartSecretSynced verifies that the given secret exists in every tenant namespace on the platform cluster.
func chartSecretSynced(secretName string) features.Func {
	return func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		secList := &corev1.SecretList{}
		namespaces := &corev1.NamespaceList{}
		if err := c.Client().Resources().List(ctx, namespaces); err != nil {
			t.Error(err)
			return ctx
		}
		for _, ns := range namespaces.Items {
			if !strings.HasPrefix(ns.Name, "mcp--") {
				continue
			}
			secList.Items = append(secList.Items, corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: ns.Name,
				},
			})
		}
		if len(secList.Items) == 0 {
			t.Error("no tenant namespaces (mcp--*) found on platform cluster")
			return ctx
		}
		if err := wait.For(conditions.New(c.Client().Resources()).ResourcesFound(secList), wait.WithTimeout(2*time.Minute)); err != nil {
			t.Errorf("chart pull secret %s not found in tenant namespaces: %v", secretName, err)
		}
		return ctx
	}
}
