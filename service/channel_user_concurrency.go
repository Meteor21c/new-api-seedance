package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
)

const channelUserConcurrencyLeaseTTL = 5 * time.Minute

const acquireChannelUserConcurrencyScript = `
local redis_time = redis.call("TIME")
local now = redis_time[1] * 1000 + math.floor(redis_time[2] / 1000)
redis.call("ZREMRANGEBYSCORE", KEYS[1], "-inf", now)
if redis.call("ZCARD", KEYS[1]) >= tonumber(ARGV[2]) then
  return 0
end
redis.call("ZADD", KEYS[1], now + tonumber(ARGV[3]), ARGV[1])
redis.call("PEXPIRE", KEYS[1], tonumber(ARGV[3]) * 2)
return 1`

const renewChannelUserConcurrencyScript = `
if not redis.call("ZSCORE", KEYS[1], ARGV[1]) then
  return 0
end
local redis_time = redis.call("TIME")
local now = redis_time[1] * 1000 + math.floor(redis_time[2] / 1000)
redis.call("ZADD", KEYS[1], now + tonumber(ARGV[2]), ARGV[1])
redis.call("PEXPIRE", KEYS[1], tonumber(ARGV[2]) * 2)
return 1`

const releaseChannelUserConcurrencyScript = `
local removed = redis.call("ZREM", KEYS[1], ARGV[1])
if redis.call("ZCARD", KEYS[1]) == 0 then
  redis.call("DEL", KEYS[1])
end
return removed`

type localChannelUserConcurrencyStore struct {
	sync.Mutex
	owners map[string]map[string]struct{}
}

var localChannelUserConcurrency = localChannelUserConcurrencyStore{
	owners: make(map[string]map[string]struct{}),
}

type ChannelUserConcurrencyLease struct {
	key       string
	owner     string
	redis     bool
	stopRenew func()
	release   sync.Once
}

func channelUserConcurrencyKey(channelID, userID int) string {
	return fmt.Sprintf("channel:user-concurrency:v1:%d:%d", channelID, userID)
}

func acquireLocalChannelUserConcurrency(key, owner string, limit int) bool {
	localChannelUserConcurrency.Lock()
	defer localChannelUserConcurrency.Unlock()

	owners := localChannelUserConcurrency.owners[key]
	if len(owners) >= limit {
		return false
	}
	if owners == nil {
		owners = make(map[string]struct{})
		localChannelUserConcurrency.owners[key] = owners
	}
	owners[owner] = struct{}{}
	return true
}

func releaseLocalChannelUserConcurrency(key, owner string) {
	localChannelUserConcurrency.Lock()
	defer localChannelUserConcurrency.Unlock()

	owners := localChannelUserConcurrency.owners[key]
	delete(owners, owner)
	if len(owners) == 0 {
		delete(localChannelUserConcurrency.owners, key)
	}
}

// AcquireChannelUserConcurrency reserves one active conversation slot for a
// user on a channel. Redis provides cross-process atomicity and expiring crash
// recovery. If Redis is unavailable, the process-local fallback stores only
// currently active limited requests and releases them with the request.
func AcquireChannelUserConcurrency(ctx context.Context, channelID, userID, limit int) (*ChannelUserConcurrencyLease, bool) {
	if channelID <= 0 || userID <= 0 || limit <= 0 {
		return nil, true
	}
	if limit > dto.MaxChannelUserConcurrency {
		limit = dto.MaxChannelUserConcurrency
	}

	key := channelUserConcurrencyKey(channelID, userID)
	owner := uuid.NewString()
	if common.RedisEnabled && common.RDB != nil {
		redisCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		acquired, err := common.RDB.Eval(
			redisCtx,
			acquireChannelUserConcurrencyScript,
			[]string{key},
			owner,
			limit,
			channelUserConcurrencyLeaseTTL.Milliseconds(),
		).Int64()
		cancel()
		if err == nil {
			if acquired == 0 {
				return nil, false
			}
			lease := &ChannelUserConcurrencyLease{key: key, owner: owner, redis: true}
			lease.stopRenew = keepChannelUserConcurrencyAlive(key, owner)
			return lease, true
		}
		common.SysLog(fmt.Sprintf("channel user concurrency Redis acquire failed, using local fallback: %v", err))
	}

	if !acquireLocalChannelUserConcurrency(key, owner, limit) {
		return nil, false
	}
	return &ChannelUserConcurrencyLease{key: key, owner: owner}, true
}

func keepChannelUserConcurrencyAlive(key, owner string) func() {
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(channelUserConcurrencyLeaseTTL / 3)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if common.RDB == nil {
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				result, err := common.RDB.Eval(
					ctx,
					renewChannelUserConcurrencyScript,
					[]string{key},
					owner,
					channelUserConcurrencyLeaseTTL.Milliseconds(),
				).Int64()
				cancel()
				if err != nil {
					common.SysLog(fmt.Sprintf("channel user concurrency Redis renewal failed: %v", err))
					continue
				}
				if result == 0 {
					return
				}
			case <-stop:
				return
			}
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() { close(stop) })
	}
}

func (lease *ChannelUserConcurrencyLease) Release() {
	if lease == nil {
		return
	}
	lease.release.Do(func() {
		if lease.stopRenew != nil {
			lease.stopRenew()
		}
		if lease.redis && common.RDB != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			err := common.RDB.Eval(
				ctx,
				releaseChannelUserConcurrencyScript,
				[]string{lease.key},
				lease.owner,
			).Err()
			cancel()
			if err != nil && err != redis.Nil {
				common.SysLog(fmt.Sprintf("channel user concurrency Redis release failed: %v", err))
			}
			return
		}
		releaseLocalChannelUserConcurrency(lease.key, lease.owner)
	})
}
