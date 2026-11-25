package coordination

import (
	"context"
	"fmt"
)

// CacheGetBounded checks the value's length in the same Redis operation that
// reads it. A value larger than maxBytes is not returned to the client.
// The reported size lets the caller admit a later bounded read.
func (r *Redis) CacheGetBounded(ctx context.Context, key string, maxBytes int64) ([]byte, int64, error) {
	if maxBytes < 0 {
		return nil, 0, fmt.Errorf("negative cache byte limit")
	}
	result, err := r.client.Eval(ctx, `
local n = redis.call('STRLEN', KEYS[1])
if n == 0 or n > tonumber(ARGV[1]) then return {n} end
return {n, redis.call('GET', KEYS[1])}
`, []string{key}, maxBytes).Slice()
	if err != nil {
		return nil, 0, err
	}
	if len(result) < 1 || len(result) > 2 {
		return nil, 0, fmt.Errorf("invalid bounded cache reply")
	}
	size, ok := result[0].(int64)
	if !ok || size < 0 {
		return nil, 0, fmt.Errorf("invalid bounded cache length")
	}
	if len(result) == 1 {
		return nil, size, nil
	}
	value, ok := result[1].(string)
	if !ok || int64(len(value)) != size || size > maxBytes {
		return nil, 0, fmt.Errorf("invalid bounded cache payload")
	}
	return []byte(value), size, nil
}
