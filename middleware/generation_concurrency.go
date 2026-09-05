package middleware

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

const (
	imageGenerationLockTTL = 20 * time.Minute
	videoSubmissionLockTTL = 2 * time.Minute
)

type generationLockStore struct {
	sync.Mutex
	owners map[string]string
}

var localGenerationLocks = generationLockStore{owners: make(map[string]string)}

func acquireLocalGenerationLock(key string, owner string) bool {
	localGenerationLocks.Lock()
	defer localGenerationLocks.Unlock()
	if _, exists := localGenerationLocks.owners[key]; exists {
		return false
	}
	localGenerationLocks.owners[key] = owner
	return true
}

func releaseLocalGenerationLock(key string, owner string) {
	localGenerationLocks.Lock()
	defer localGenerationLocks.Unlock()
	if localGenerationLocks.owners[key] == owner {
		delete(localGenerationLocks.owners, key)
	}
}

func acquireGenerationLock(key string, owner string, ttl time.Duration) (bool, bool) {
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		acquired, err := common.RDB.SetNX(ctx, key, owner, ttl).Result()
		if err == nil {
			return acquired, true
		}
		common.SysLog(fmt.Sprintf("generation concurrency Redis lock failed, using local fallback: %v", err))
	}
	return acquireLocalGenerationLock(key, owner), false
}

func releaseGenerationLock(key string, owner string, redisLock bool) {
	if redisLock && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		const compareAndDelete = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0`
		if err := common.RDB.Eval(ctx, compareAndDelete, []string{key}, owner).Err(); err != nil && err != redis.Nil {
			common.SysLog(fmt.Sprintf("generation concurrency Redis unlock failed: %v", err))
		}
		return
	}
	releaseLocalGenerationLock(key, owner)
}

func keepGenerationLockAlive(key string, owner string, ttl time.Duration, redisLock bool) func() {
	if !redisLock || common.RDB == nil {
		return func() {}
	}

	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(ttl / 3)
		defer ticker.Stop()
		const compareAndExpire = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("PEXPIRE", KEYS[1], ARGV[2])
end
return 0`
		for {
			select {
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				result, err := common.RDB.Eval(ctx, compareAndExpire, []string{key}, owner, ttl.Milliseconds()).Int64()
				cancel()
				if err != nil {
					common.SysLog(fmt.Sprintf("generation concurrency Redis renewal failed: %v", err))
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

func activeVideoLookback() time.Duration {
	if constant.TaskTimeoutMinutes > 0 {
		return time.Duration(constant.TaskTimeoutMinutes) * time.Minute
	}
	return 24 * time.Hour
}

// GenerationConcurrency allows one active request per user and media kind.
// Image requests are synchronous, so the lock lives for the handler duration.
// Video requests are asynchronous: a short lock closes the simultaneous-submit
// race, while the task table blocks subsequent submissions until the previous
// task reaches SUCCESS or FAILURE. The same middleware covers dashboard and
// token/MCP routes.
func GenerationConcurrency(kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Jimeng's official result-query endpoint enters through a POST route and
		// its conversion middleware rewrites the request to GET. Only creation
		// requests should participate in generation concurrency control.
		if c.Request.Method != http.MethodPost {
			c.Next()
			return
		}

		userID := c.GetInt("id")
		if userID <= 0 {
			c.Next()
			return
		}

		lockKey := fmt.Sprintf("generation:active:%s:%d", kind, userID)
		owner, err := common.GenerateRandomCharsKey(24)
		if err != nil {
			abortWithOpenAiMessage(c, http.StatusInternalServerError, common.TranslateMessage(c, i18n.MsgGenerateConcurrencyCheckFailed))
			return
		}
		lockTTL := imageGenerationLockTTL
		if kind == "video" {
			lockTTL = videoSubmissionLockTTL
		}
		acquired, redisLock := acquireGenerationLock(lockKey, owner, lockTTL)
		if !acquired {
			abortWithOpenAiMessage(c, http.StatusConflict, common.TranslateMessage(c, i18n.MsgGenerateAlreadyInProgress))
			return
		}
		defer releaseGenerationLock(lockKey, owner, redisLock)
		stopRenewal := keepGenerationLockAlive(lockKey, owner, lockTTL, redisLock)
		defer stopRenewal()

		if kind == "video" {
			cutoff := time.Now().Add(-activeVideoLookback()).Unix()
			active, queryErr := model.HasActiveVideoTaskForUser(userID, cutoff)
			if queryErr != nil {
				abortWithOpenAiMessage(c, http.StatusInternalServerError, common.TranslateMessage(c, i18n.MsgGenerateConcurrencyCheckFailed))
				return
			}
			if active {
				abortWithOpenAiMessage(c, http.StatusConflict, common.TranslateMessage(c, i18n.MsgGenerateVideoAlreadyInProgress))
				return
			}
		}

		c.Next()
	}
}
