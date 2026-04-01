package membership

import (
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/hashicorp/memberlist"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildKeyring_NoKeys(t *testing.T) {
	kr, err := BuildKeyring(nil)
	assert.NoError(t, err)
	assert.Nil(t, kr)

	kr, err = BuildKeyring([]string{})
	assert.NoError(t, err)
	assert.Nil(t, kr)
}

func TestBuildKeyring_SingleKey(t *testing.T) {
	key := "12345678901234561234567890123456" // 32 bytes
	kr, err := BuildKeyring([]string{key})
	require.NoError(t, err)
	require.NotNil(t, kr)

	assert.Equal(t, []byte(key), kr.GetPrimaryKey())
	assert.Len(t, kr.GetKeys(), 1)
}

func TestBuildKeyring_MultipleKeys(t *testing.T) {
	primary := "PrimaryKey_32Bytes______________"   // 32 bytes
	secondary := "SecondaryKey32Bytes_____________" // 32 bytes

	kr, err := BuildKeyring([]string{primary, secondary})
	require.NoError(t, err)
	require.NotNil(t, kr)

	assert.Equal(t, []byte(primary), kr.GetPrimaryKey())
	assert.Len(t, kr.GetKeys(), 2)
}

func TestBuildKeyring_InvalidKeyLength(t *testing.T) {
	_, err := BuildKeyring([]string{"too-short"})
	assert.Error(t, err)
}

// startEncryptedNode creates a raw memberlist node with encryption for testing.
// Using memberlist directly (not Cluster) to control Name independently from bind address.
func startEncryptedNode(t *testing.T, name string, port int, secretKeys []string) *memberlist.Memberlist {
	t.Helper()
	cfg := memberlist.DefaultLANConfig()
	cfg.Name = name
	cfg.BindAddr = "127.0.0.1"
	cfg.BindPort = port
	cfg.AdvertiseAddr = "127.0.0.1"
	cfg.AdvertisePort = port
	cfg.LogOutput = &slogWriter{logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError})).With("node", name)}

	if len(secretKeys) > 0 {
		kr, err := BuildKeyring(secretKeys)
		require.NoError(t, err)
		cfg.Keyring = kr
	}

	ml, err := memberlist.Create(cfg)
	require.NoError(t, err)
	return ml
}

// TestKeyRotation_Integration simulates a zero-downtime key rotation across
// two cluster nodes using real memberlist instances.
func TestKeyRotation_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	key1 := "RotationTestKey1_32Bytes________" // 32 bytes
	key2 := "RotationTestKey2_32Bytes________" // 32 bytes

	// --- Phase 1: Initial State ---
	// Both nodes share Key1
	t.Run("Phase1_InitialState", func(t *testing.T) {
		nodeA := startEncryptedNode(t, "phase1-a", 17960, []string{key1})
		defer func() { _ = nodeA.Shutdown() }()

		nodeB := startEncryptedNode(t, "phase1-b", 17961, []string{key1})
		defer func() { _ = nodeB.Shutdown() }()

		n, err := nodeB.Join([]string{"127.0.0.1:17960"})
		require.NoError(t, err)
		require.Equal(t, 1, n)
		time.Sleep(500 * time.Millisecond)

		assert.Equal(t, 2, nodeA.NumMembers(), "Phase 1: Node A should see 2 members")
		assert.Equal(t, 2, nodeB.NumMembers(), "Phase 1: Node B should see 2 members")
	})

	// --- Phase 2: Staging (add Key2) ---
	// Node A: Key1 primary, Key2 secondary
	// Node B: Key2 primary, Key1 secondary
	t.Run("Phase2_Staging", func(t *testing.T) {
		nodeA := startEncryptedNode(t, "phase2-a", 17962, []string{key1, key2})
		defer func() { _ = nodeA.Shutdown() }()

		nodeB := startEncryptedNode(t, "phase2-b", 17963, []string{key2, key1})
		defer func() { _ = nodeB.Shutdown() }()

		n, err := nodeB.Join([]string{"127.0.0.1:17962"})
		require.NoError(t, err)
		require.Equal(t, 1, n)
		time.Sleep(500 * time.Millisecond)

		assert.Equal(t, 2, nodeA.NumMembers(), "Phase 2: Node A should see 2 members")
		assert.Equal(t, 2, nodeB.NumMembers(), "Phase 2: Node B should see 2 members")
	})

	// --- Phase 3: Final switch ---
	// Both nodes: Key2 primary, Key1 secondary
	t.Run("Phase3_FinalSwitch", func(t *testing.T) {
		nodeA := startEncryptedNode(t, "phase3-a", 17964, []string{key2, key1})
		defer func() { _ = nodeA.Shutdown() }()

		nodeB := startEncryptedNode(t, "phase3-b", 17965, []string{key2, key1})
		defer func() { _ = nodeB.Shutdown() }()

		n, err := nodeB.Join([]string{"127.0.0.1:17964"})
		require.NoError(t, err)
		require.Equal(t, 1, n)
		time.Sleep(500 * time.Millisecond)

		assert.Equal(t, 2, nodeA.NumMembers(), "Phase 3: Node A should see 2 members")
		assert.Equal(t, 2, nodeB.NumMembers(), "Phase 3: Node B should see 2 members")
	})

	// --- Phase 4: Remove old key ---
	// Both nodes: Key2 only
	t.Run("Phase4_RemoveOldKey", func(t *testing.T) {
		nodeA := startEncryptedNode(t, "phase4-a", 17966, []string{key2})
		defer func() { _ = nodeA.Shutdown() }()

		nodeB := startEncryptedNode(t, "phase4-b", 17967, []string{key2})
		defer func() { _ = nodeB.Shutdown() }()

		n, err := nodeB.Join([]string{"127.0.0.1:17966"})
		require.NoError(t, err)
		require.Equal(t, 1, n)
		time.Sleep(500 * time.Millisecond)

		assert.Equal(t, 2, nodeA.NumMembers(), "Phase 4: Node A should see 2 members")
		assert.Equal(t, 2, nodeB.NumMembers(), "Phase 4: Node B should see 2 members")
	})
}

// TestEncrypted_CannotJoinUnencrypted verifies that an encrypted node cannot
// join an unencrypted cluster (mismatched encryption).
func TestEncrypted_CannotJoinUnencrypted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	key := "EncryptedTestKey_32Bytes________" // 32 bytes

	// Unencrypted node
	nodeA := startEncryptedNode(t, "unenc-a", 17968, nil)
	defer func() { _ = nodeA.Shutdown() }()

	// Encrypted node
	nodeB := startEncryptedNode(t, "enc-b", 17969, []string{key})
	defer func() { _ = nodeB.Shutdown() }()

	// Encrypted node tries to join unencrypted — should fail or not converge
	_, _ = nodeB.Join([]string{fmt.Sprintf("127.0.0.1:%d", 17968)})
	time.Sleep(500 * time.Millisecond)

	// They should NOT converge to 2 members since encryption is mismatched
	assert.Equal(t, 1, nodeA.NumMembers(), "Unencrypted node should only see itself")
}
