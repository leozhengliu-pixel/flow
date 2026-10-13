package coordination

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// tokenBucketScript implements a leaky (token) bucket that refills at
// capacity/period. A zero cost only reads the current level. It returns
// {allowed, remaining*1000} so fractional refills survive the integer reply.
var tokenBucketScript = redis.NewScript(`
local capacity = tonumber(ARGV[1])
local period = tonumber(ARGV[2])
local cost = tonumber(ARGV[3])
local now = tonumber(ARGV[4])
local state = redis.call('HMGET', KEYS[1], 'tokens', 'updated')
local tokens = tonumber(state[1])
local updated = tonumber(state[2])
if tokens == nil or updated == nil then
  tokens = capacity
  updated = now
end
if now > updated then
  tokens = math.min(capacity, tokens + (now - updated) * capacity / period)
  updated = now
end
local allowed = 0
if tokens >= cost then
  tokens = tokens - cost
  allowed = 1
end
redis.call('HSET', KEYS[1], 'tokens', tostring(tokens), 'updated', tostring(updated))
redis.call('PEXPIRE', KEYS[1], period)
return {allowed, math.floor(tokens * 1000)}`)

// TakeTokens consumes cost tokens from the shared bucket named by key. All API
// instances share the bucket, so per-credential limits hold across pods.
func (r *Redis) TakeTokens(ctx context.Context, key string, capacity float64, period time.Duration, cost float64, now time.Time) (bool, float64, error) {
	values, err := tokenBucketScript.Run(ctx, r.client, []string{r.key("bucket", hash(key))},
		strconv.FormatFloat(capacity, 'f', -1, 64), period.Milliseconds(), strconv.FormatFloat(cost, 'f', -1, 64), now.UnixMilli()).Int64Slice()
	if err != nil {
		return false, 0, err
	}
	if len(values) != 2 {
		return false, 0, redis.Nil
	}
	return values[0] == 1, float64(values[1]) / 1000, nil
}
