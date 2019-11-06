package testsuites

import (
	"fmt"
	"testing"

	"github.com/operator-framework/operator-marketplace/pkg/apis/operators/shared"
	"github.com/operator-framework/operator-marketplace/pkg/apis/operators/v1"
	"github.com/operator-framework/operator-marketplace/test/helpers"
	"github.com/operator-framework/operator-sdk/pkg/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// endpointType represents the endpoint we currently support
	endpointType string = "appregistry"
	// marketplaceRegistryNamespace is the e2e registry namespace in quay
	marketplaceRegistryNamespace string = "marketplace_e2e"
)

// InvalidOpSrc tests OperatorSources created with invalid values
// to make sure the expected failure state is reached
func InvalidOpSrc(t *testing.T) {
	t.Run("invalid-endpoint", testOpSrcWithInvalidEndpoint)
	t.Run("invalid-url", testOpSrcWithInvalidURL)
	t.Run("nonexistent-registry-namespace", testOpSrcWithNonexistentRegistryNamespace)
	t.Run("object-in-other-namespace", testOpSrcInOtherNamespace)
}

// Create OperatorSource with invalid endpoint
// Expected result: OperatorSource stuck in configuring state
func testOpSrcWithInvalidEndpoint(t *testing.T) {
	opSrcName := "invalid-endpoint-opsrc"
	// invalidEndpoint is the invalid endpoint for the OperatorSource
	invalidEndpoint := "https://not-quay.io/cnr"

	ctx := test.NewTestCtx(t)
	defer ctx.Cleanup()

	// Get global framework variables
	client := test.Global.Client

	// Get test namespace
	namespace, err := ctx.GetNamespace()
	require.NoError(t, err, "Could not get namespace")

	invalidURLOperatorSource := &v1.OperatorSource{
		TypeMeta: metav1.TypeMeta{
			Kind: v1.OperatorSourceKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      opSrcName,
			Namespace: namespace,
		},
		Spec: v1.OperatorSourceSpec{
			Type:              endpointType,
			Endpoint:          invalidEndpoint,
			RegistryNamespace: marketplaceRegistryNamespace,
		},
	}
	err = helpers.CreateRuntimeObject(client, ctx, invalidURLOperatorSource)
	require.NoError(t, err, "Could not create OperatorSource")

	// Check that OperatorSource is in "Configuring" state with appropriate message
	expectedPhase := "Configuring"
	_, err = helpers.WaitForOpSrcExpectedPhaseAndMessage(client, opSrcName, namespace, expectedPhase, "no such host")
	assert.NoError(t, err, fmt.Sprintf("OperatorSource never reached expected phase/message, expected %v", expectedPhase))

	// Delete the OperatorSource
	err = helpers.DeleteRuntimeObject(client, invalidURLOperatorSource)
	require.NoError(t, err, "Could not delete OperatorSource")
}

// Create OperatorSource with invalid URL
// Expected result: OperatorSource reaches failed state
func testOpSrcWithInvalidURL(t *testing.T) {
	opSrcName := "invalid-url-opsrc"
	// invalidURL is an invalid URI
	invalidURL := "not-a-url"

	ctx := test.NewTestCtx(t)
	defer ctx.Cleanup()

	// Get global framework variables
	client := test.Global.Client

	// Get test namespace
	namespace, err := ctx.GetNamespace()
	require.NoError(t, err, "Could not get namespace")

	invalidURLOperatorSource := &v1.OperatorSource{
		TypeMeta: metav1.TypeMeta{
			Kind: v1.OperatorSourceKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      opSrcName,
			Namespace: namespace,
		},
		Spec: v1.OperatorSourceSpec{
			Type:              endpointType,
			Endpoint:          invalidURL,
			RegistryNamespace: marketplaceRegistryNamespace,
		},
	}
	err = helpers.CreateRuntimeObject(client, ctx, invalidURLOperatorSource)
	require.NoError(t, err, "Could not create OperatorSource")

	// Check that OperatorSource reaches "Failed" state eventually
	expectedPhase := "Failed"
	_, err = helpers.WaitForOpSrcExpectedPhaseAndMessage(client, opSrcName, namespace, expectedPhase, "Invalid OperatorSource endpoint")
	assert.NoError(t, err, fmt.Sprintf("OperatorSource never reached expected phase/message, expected %v", expectedPhase))

	// Delete the OperatorSource
	err = helpers.DeleteRuntimeObject(client, invalidURLOperatorSource)
	require.NoError(t, err, "Could not delete OperatorSource")
}

// Create OperatorSource with valid URL but non-existent registry namespace
// Expected result: OperatorSource reaches failed state
func testOpSrcWithNonexistentRegistryNamespace(t *testing.T) {
	opSrcName := "nonexistent-namespace-opsrc"
	// validURL is a valid endpoint for the OperatorSource
	validURL := "https://quay.io/cnr"

	// nonexistentRegistryNamespace is a namespace that does not exist
	// on the app registry
	nonexistentRegistryNamespace := "not-existent-namespace"

	ctx := test.NewTestCtx(t)
	defer ctx.Cleanup()

	// Get global framework variables
	client := test.Global.Client

	// Get test namespace
	namespace, err := ctx.GetNamespace()
	require.NoError(t, err, "Could not get namespace")
	nonexistentRegistryNamespaceOperatorSource := &v1.OperatorSource{
		TypeMeta: metav1.TypeMeta{
			Kind: v1.OperatorSourceKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      opSrcName,
			Namespace: namespace,
		},
		Spec: v1.OperatorSourceSpec{
			Type:              endpointType,
			Endpoint:          validURL,
			RegistryNamespace: nonexistentRegistryNamespace,
		},
	}
	err = helpers.CreateRuntimeObject(client, ctx, nonexistentRegistryNamespaceOperatorSource)
	require.NoError(t, err, "Could not create OperatorSource")

	// Check that OperatorSource reaches "Failed" state eventually
	expectedPhase := "Failed"
	_, err = helpers.WaitForOpSrcExpectedPhaseAndMessage(client, opSrcName, namespace, expectedPhase, "The OperatorSource endpoint returned an empty manifest list")
	assert.NoError(t, err, fmt.Sprintf("OperatorSource never reached expected phase/message, expected %v", expectedPhase))

	// Delete the OperatorSource
	err = helpers.DeleteRuntimeObject(client, nonexistentRegistryNamespaceOperatorSource)
	require.NoError(t, err, "Could not delete OperatorSource")
}

// testOpSrcInOtherNamespace creates an OperatorSource in the default namespace
// and forces it through all the phases
// Expected result: OperatorSource always reaches failed state
func testOpSrcInOtherNamespace(t *testing.T) {
	ctx := test.NewTestCtx(t)
	defer ctx.Cleanup()

	// Get global framework variables
	client := test.Global.Client

	// Create the OperatorSource in the default namespace
	namespace := "default"
	opSrcName := "other-namespace-opsrc"
	otherNamespaceOperatorSource := &v1.OperatorSource{
		TypeMeta: metav1.TypeMeta{
			Kind: v1.OperatorSourceKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      opSrcName,
			Namespace: namespace,
		},
		Spec: v1.OperatorSourceSpec{
			Type:              endpointType,
			Endpoint:          "https://quay.io/cnr",
			RegistryNamespace: "marketplace_e2e",
		},
	}
	err := helpers.CreateRuntimeObject(client, ctx, otherNamespaceOperatorSource)
	require.NoError(t, err, "Could not create OperatorSource")

	expectedPhase := "Failed"
	opsrc, err := helpers.WaitForOpSrcExpectedPhaseAndMessage(client, opSrcName, namespace, expectedPhase,
		"Will only reconcile resources in the operator's namespace")
	assert.NoError(t, err, fmt.Sprintf("OperatorSource never reached expected phase/message, expected %s", expectedPhase))
	require.NotNil(t, opsrc, "Could not retrieve OperatorSource")

	// Force the OperatorSource status into various phases other than "Failed" and "Initial"
	for _, phase := range []string{"Configuring", "Succeeded", "Validating", "Purging"} {
		opsrc.Status = v1.OperatorSourceStatus{
			CurrentPhase: shared.ObjectPhase{
				Phase: shared.Phase{
					Name: phase,
				},
			},
		}
		err = helpers.UpdateRuntimeObject(client, opsrc)
		require.NoError(t, err, "Could not update OperatorSource")
		opsrc, err = helpers.WaitForOpSrcExpectedPhaseAndMessage(client, opSrcName, namespace, expectedPhase,
			"Will only reconcile resources in the operator's namespace")
		assert.NoError(t, err, fmt.Sprintf("OperatorSource never reached expected phase/message for inserted phase %s, expected %s", phase, expectedPhase))
	}

}
