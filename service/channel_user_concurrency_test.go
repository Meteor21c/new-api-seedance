package service

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func useLocalChannelUserConcurrency(t *testing.T) {
	t.Helper()
	originalRedisEnabled := common.RedisEnabled
	originalRDB := common.RDB
	common.RedisEnabled = false
	common.RDB = nil
	localChannelUserConcurrency.Lock()
	localChannelUserConcurrency.owners = make(map[string]map[string]struct{})
	localChannelUserConcurrency.Unlock()
	t.Cleanup(func() {
		localChannelUserConcurrency.Lock()
		localChannelUserConcurrency.owners = make(map[string]map[string]struct{})
		localChannelUserConcurrency.Unlock()
		common.RedisEnabled = originalRedisEnabled
		common.RDB = originalRDB
	})
}

func useRedisChannelUserConcurrency(t *testing.T) {
	t.Helper()
	originalRedisEnabled := common.RedisEnabled
	originalRDB := common.RDB
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RedisEnabled = true
	common.RDB = client
	t.Cleanup(func() {
		common.RedisEnabled = originalRedisEnabled
		common.RDB = originalRDB
		require.NoError(t, client.Close())
	})
}

func TestChannelUserConcurrencyLocalLimitAndRelease(t *testing.T) {
	useLocalChannelUserConcurrency(t)
	ctx := context.Background()

	first, acquired := AcquireChannelUserConcurrency(ctx, 11, 21, 2)
	require.True(t, acquired)
	require.NotNil(t, first)
	second, acquired := AcquireChannelUserConcurrency(ctx, 11, 21, 2)
	require.True(t, acquired)
	require.NotNil(t, second)

	blocked, acquired := AcquireChannelUserConcurrency(ctx, 11, 21, 2)
	assert.False(t, acquired)
	assert.Nil(t, blocked)

	otherUser, acquired := AcquireChannelUserConcurrency(ctx, 11, 22, 2)
	require.True(t, acquired)
	require.NotNil(t, otherUser)
	otherChannel, acquired := AcquireChannelUserConcurrency(ctx, 12, 21, 2)
	require.True(t, acquired)
	require.NotNil(t, otherChannel)

	first.Release()
	replacement, acquired := AcquireChannelUserConcurrency(ctx, 11, 21, 2)
	require.True(t, acquired)
	require.NotNil(t, replacement)

	second.Release()
	otherUser.Release()
	otherChannel.Release()
	replacement.Release()
	assert.Empty(t, localChannelUserConcurrency.owners)
}

func TestChannelUserConcurrencyZeroIsUnlimitedAndAllocationFree(t *testing.T) {
	useLocalChannelUserConcurrency(t)

	lease, acquired := AcquireChannelUserConcurrency(context.Background(), 11, 21, 0)
	assert.True(t, acquired)
	assert.Nil(t, lease)
	assert.Empty(t, localChannelUserConcurrency.owners)
}

func TestChannelUserConcurrencyRedisLimitAndRelease(t *testing.T) {
	useRedisChannelUserConcurrency(t)
	ctx := context.Background()

	first, acquired := AcquireChannelUserConcurrency(ctx, 31, 41, 1)
	require.True(t, acquired)
	require.NotNil(t, first)
	blocked, acquired := AcquireChannelUserConcurrency(ctx, 31, 41, 1)
	assert.False(t, acquired)
	assert.Nil(t, blocked)

	first.Release()
	replacement, acquired := AcquireChannelUserConcurrency(ctx, 31, 41, 1)
	require.True(t, acquired)
	require.NotNil(t, replacement)
	replacement.Release()
}
