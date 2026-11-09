                                                           

package committee

import (
	"dex/frost/runtime"
	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func validRankedProviderTop(indices []uint64, addresses []string, height uint64) *pb.FrostActiveMiners {
	top := &pb.FrostActiveMiners{
		Height:             height,
		UpdateHeight:       height,
		Indices:            append([]uint64(nil), indices...),
		Addresses:          append([]string(nil), addresses...),
		PublicKeys:         make([][]byte, len(addresses)),
		MaturedStakes:      make([]uint64, len(addresses)),
		ActiveSinceHeights: make([]uint64, len(addresses)),
		EligibleCount:      uint32(len(addresses)),
	}
	for i := range addresses {
		top.PublicKeys[i] = []byte{byte(i%255 + 1)}
		top.MaturedStakes[i] = protocol.MinMinerStakeRaw + uint64(len(addresses)-i)
		top.ActiveSinceHeights[i] = 1
	}
	return top
}

                                          
type mockStateReader struct {
	data map[string][]byte
}

func newMockStateReader() *mockStateReader {
	return &mockStateReader{
		data: make(map[string][]byte),
	}
}

func (m *mockStateReader) Get(key string) ([]byte, bool, error) {
	data, exists := m.data[key]
	return data, exists, nil
}

func (m *mockStateReader) Scan(prefix string, fn func(k string, v []byte) bool) error {
	for k, v := range m.data {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			if !fn(k, v) {
				break
			}
		}
	}
	return nil
}

func (m *mockStateReader) Set(key string, value []byte) {
	m.data[key] = value
}

                                                       
func TestVaultCommitteeProvider_GetTopK(t *testing.T) {
	reader := newMockStateReader()

	                     
	topK := validRankedProviderTop(
		[]uint64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
		[]string{"addr1", "addr2", "addr3", "addr4", "addr5", "addr6", "addr7", "addr8", "addr9", "addr10"},
		100,
	)
	data, err := proto.Marshal(topK)
	require.NoError(t, err)
	reader.Set(keys.KeyFrostActiveMiners(), data)

	                  
	provider := NewDefaultVaultCommitteeProvider(reader, DefaultVaultCommitteeProviderConfig())

	               
	result, err := provider.GetTopK()
	require.NoError(t, err)
	assert.Equal(t, uint64(100), result.Height)
	assert.Equal(t, 10, len(result.Indices))
	assert.Equal(t, "addr1", result.Addresses[0])
}

                                                                     
func TestVaultCommitteeProvider_GetVaultConfig(t *testing.T) {
	reader := newMockStateReader()

	                            
	cfg := &pb.FrostVaultConfig{
		Chain:          "btc",
		SignAlgo:       pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		VaultCount:     50,
		CommitteeSize:  200,
		ThresholdRatio: 0.67,
	}
	data, err := proto.Marshal(cfg)
	require.NoError(t, err)
	reader.Set(keys.KeyFrostVaultConfig("btc", 0), data)

	                  
	provider := NewDefaultVaultCommitteeProvider(reader, DefaultVaultCommitteeProviderConfig())

	               
	result, err := provider.GetVaultConfig("btc")
	require.NoError(t, err)
	assert.Equal(t, "btc", result.Chain)
	assert.Equal(t, uint32(50), result.VaultCount)
	assert.Equal(t, uint32(200), result.CommitteeSize)
}

func TestVaultCommitteeProvider_VaultCommitteeRejectsMissingFrozenSnapshot(t *testing.T) {
	reader := newMockStateReader()

	                                     
	indices := make([]uint64, 100)
	addresses := make([]string, 100)
	for i := 0; i < 100; i++ {
		indices[i] = uint64(i)
		addresses[i] = fmt.Sprintf("addr-%03d", i)
	}

	topK := validRankedProviderTop(indices, addresses, 100)
	data, err := proto.Marshal(topK)
	require.NoError(t, err)
	reader.Set(keys.KeyFrostActiveMiners(), data)

	provider := NewDefaultVaultCommitteeProvider(reader, DefaultVaultCommitteeProviderConfig())
	_, err = provider.VaultCommittee("btc", 0, 1)
	require.ErrorContains(t, err, "frozen vault committee snapshot not found")
}

                                                                   
func TestVaultCommitteeProvider_CalculateThreshold(t *testing.T) {
	reader := newMockStateReader()
	state := &pb.FrostVaultState{
		Chain:              "btc",
		VaultId:            0,
		CommitteeMembers:   []string{"a", "b", "c", "d", "e", "f", "g", "h"},
		CommitteeThreshold: 6,
	}
	stateData, err := proto.Marshal(state)
	require.NoError(t, err)
	reader.Set(keys.KeyFrostVaultState("btc", 0), stateData)

	provider := NewDefaultVaultCommitteeProvider(reader, DefaultVaultCommitteeProviderConfig())

	threshold, err := provider.CalculateThreshold("btc", 0)
	require.NoError(t, err)
	assert.Equal(t, 6, threshold)
}

func TestVaultCommitteeProvider_CalculateThreshold_RejectsMissingFrozenThreshold(t *testing.T) {
	reader := newMockStateReader()
	state := &pb.FrostVaultState{
		Chain:            "btc",
		VaultId:          0,
		CommitteeMembers: []string{"a", "b", "c", "d"},
	}
	stateData, err := proto.Marshal(state)
	require.NoError(t, err)
	reader.Set(keys.KeyFrostVaultState("btc", 0), stateData)

	providerCfg := DefaultVaultCommitteeProviderConfig()
	providerCfg.DefaultCommitteeSize = 100
	providerCfg.DefaultThresholdRatio = 0.01
	provider := NewDefaultVaultCommitteeProvider(reader, providerCfg)

	_, err = provider.CalculateThreshold("btc", 0)
	require.ErrorContains(t, err, "invalid frozen threshold")
}

func TestVaultCommitteeProvider_CalculateThreshold_RejectsInvalidFrozenState(t *testing.T) {
	tests := map[string]*pb.FrostVaultState{
		"stored threshold mismatch": {
			Chain: "btc", VaultId: 0,
			CommitteeMembers:   []string{"a", "b", "c", "d", "e", "f", "g", "h"},
			CommitteeThreshold: 3,
		},
		"unsupported committee size": {
			Chain: "btc", VaultId: 0,
			CommitteeMembers: []string{"a", "b", "c", "d", "e"},
		},
	}
	for name, state := range tests {
		t.Run(name, func(t *testing.T) {
			reader := newMockStateReader()
			stateData, err := proto.Marshal(state)
			require.NoError(t, err)
			reader.Set(keys.KeyFrostVaultState("btc", 0), stateData)
			provider := NewDefaultVaultCommitteeProvider(reader, DefaultVaultCommitteeProviderConfig())
			_, err = provider.CalculateThreshold("btc", 0)
			require.Error(t, err)
		})
	}

	t.Run("missing state", func(t *testing.T) {
		provider := NewDefaultVaultCommitteeProvider(newMockStateReader(), DefaultVaultCommitteeProviderConfig())
		_, err := provider.CalculateThreshold("btc", 0)
		require.Error(t, err)
	})
}

func TestVaultCommitteeProvider_VaultCommittee_UsesTransitionSnapshot(t *testing.T) {
	reader := newMockStateReader()

	topK := validRankedProviderTop([]uint64{11, 22}, []string{"addrA", "addrB"}, 100)
	topK.PublicKeys = [][]byte{{0xAA}, {0xBB}}
	topData, err := proto.Marshal(topK)
	require.NoError(t, err)
	reader.Set(keys.KeyFrostActiveMiners(), topData)

	transition := &pb.VaultTransitionState{
		Chain:               "btc",
		VaultId:             1,
		EpochId:             9,
		NewCommitteeMembers: []string{"addrB", "addrC", "addrD", "addrA"},
		DkgN:                4,
		DkgThresholdT:       3,
	}
	transitionData, err := proto.Marshal(transition)
	require.NoError(t, err)
	reader.Set(keys.KeyFrostVaultTransition("btc", 1, 9), transitionData)

	accountC := &pb.Account{
		Address: "addrC",
		Index:   303,
		PublicKeys: &pb.PublicKeys{
			Keys: map[int32][]byte{
				int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX): {0xCC},
			},
		},
	}
	accountCData, err := proto.Marshal(accountC)
	require.NoError(t, err)
	reader.Set(keys.KeyAccount("addrC"), accountCData)
	accountD := proto.Clone(accountC).(*pb.Account)
	accountD.Address = "addrD"
	accountD.Index = 404
	accountD.PublicKeys.Keys[int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX)] = []byte{0xDD}
	accountDData, err := proto.Marshal(accountD)
	require.NoError(t, err)
	reader.Set(keys.KeyAccount("addrD"), accountDData)

	provider := NewDefaultVaultCommitteeProvider(reader, DefaultVaultCommitteeProviderConfig())

	committee, err := provider.VaultCommittee("btc", 1, 9)
	require.NoError(t, err)
	require.Len(t, committee, 4)

	assert.Equal(t, runtime.NodeID("addrB"), committee[0].ID)
	assert.Equal(t, uint32(22), committee[0].Index)
	assert.Equal(t, []byte{0xBB}, committee[0].PublicKey)

	assert.Equal(t, runtime.NodeID("addrC"), committee[1].ID)
	assert.Equal(t, uint32(303), committee[1].Index)
	assert.Equal(t, []byte{0xCC}, committee[1].PublicKey)
	assert.Equal(t, runtime.NodeID("addrD"), committee[2].ID)
	assert.Equal(t, runtime.NodeID("addrA"), committee[3].ID)
}

func TestVaultCommitteeProvider_VaultCommittee_UsesVaultStateSnapshot(t *testing.T) {
	reader := newMockStateReader()

	topK := validRankedProviderTop([]uint64{100, 101, 102, 103}, []string{"addrX", "addrY", "addrZ", "addrW"}, 200)
	topK.PublicKeys = [][]byte{{0x01}, {0x02}, {0x03}, {0x04}}
	topData, err := proto.Marshal(topK)
	require.NoError(t, err)
	reader.Set(keys.KeyFrostActiveMiners(), topData)

	vaultState := &pb.FrostVaultState{
		VaultId:            2,
		Chain:              "btc",
		KeyEpoch:           7,
		CommitteeMembers:   []string{"addrZ", "addrX", "addrY", "addrW"},
		CommitteeThreshold: 3,
	}
	stateData, err := proto.Marshal(vaultState)
	require.NoError(t, err)
	reader.Set(keys.KeyFrostVaultState("btc", 2), stateData)

	provider := NewDefaultVaultCommitteeProvider(reader, DefaultVaultCommitteeProviderConfig())

	committee, err := provider.VaultCommittee("btc", 2, 7)
	require.NoError(t, err)
	require.Len(t, committee, 4)

	assert.Equal(t, runtime.NodeID("addrZ"), committee[0].ID)
	assert.Equal(t, uint32(102), committee[0].Index)
	assert.Equal(t, []byte{0x03}, committee[0].PublicKey)

	assert.Equal(t, runtime.NodeID("addrX"), committee[1].ID)
	assert.Equal(t, uint32(100), committee[1].Index)
	assert.Equal(t, []byte{0x01}, committee[1].PublicKey)
	assert.Equal(t, runtime.NodeID("addrY"), committee[2].ID)
	assert.Equal(t, runtime.NodeID("addrW"), committee[3].ID)
}

func TestVaultCommitteeProvider_VaultCommitteeDoesNotFallbackToCurrentTopK(t *testing.T) {
	reader := newMockStateReader()
	indices := []uint64{4, 1, 3, 0, 2}
	topK := validRankedProviderTop(indices, []string{"addr4", "addr1", "addr3", "addr0", "addr2"}, 100)
	data, err := proto.Marshal(topK)
	require.NoError(t, err)
	reader.Set(keys.KeyFrostActiveMiners(), data)

	cfg := &pb.FrostVaultConfig{
		Chain:         "btc",
		VaultCount:    1,
		CommitteeSize: 3,
	}
	cfgData, err := proto.Marshal(cfg)
	require.NoError(t, err)
	reader.Set(keys.KeyFrostVaultConfig("btc", 0), cfgData)

	provider := NewDefaultVaultCommitteeProvider(reader, DefaultVaultCommitteeProviderConfig())
	_, err = provider.VaultCommittee("btc", 0, 1)
	require.ErrorContains(t, err, "frozen vault committee snapshot not found")
}
