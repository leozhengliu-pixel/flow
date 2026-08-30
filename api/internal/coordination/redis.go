package coordination

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"

	"github.com/redis/go-redis/v9"
)

type Config struct {
	Mode           string
	URL            string
	Addrs          []string
	Username       string
	Password       string
	DB             int
	TLS            bool
	Prefix         string
	PoolSize       int
	MinIdleConns   int
	DialTimeout    time.Duration
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
	ConnectTimeout time.Duration
	LockTTL        time.Duration
	LockWait       time.Duration
}

type EventEnvelope struct {
	Source    string               `json:"source"`
	Workspace string               `json:"workspace"`
	Event     domain.RealtimeEvent `json:"event"`
}

type Redis struct {
	client     redis.UniversalClient
	mode       string
	prefix     string
	instanceID string
	lockTTL    time.Duration
	lockWait   time.Duration
}

var (
	rateLimitScript = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
if count == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end
local ttl = redis.call('PTTL', KEYS[1])
return {count, ttl}`)
	unlockScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0`)
	refreshLockScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
return 0`)
	presenceSnapshotScript = redis.NewScript(`
local stale = redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
if #stale > 0 then redis.call('HDEL', KEYS[2], unpack(stale)) end
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
local values = redis.call('HVALS', KEYS[2])
local result = {#stale}
for _, value in ipairs(values) do table.insert(result, value) end
return result`)
)

func Open(ctx context.Context, config Config) (*Redis, error) {
	mode := strings.ToLower(strings.TrimSpace(config.Mode))
	if mode == "" || mode == "disabled" {
		return nil, nil
	}
	client, err := newClient(config)
	if err != nil {
		return nil, err
	}
	coordinator := &Redis{
		client: client, mode: mode, prefix: strings.Trim(strings.TrimSpace(config.Prefix), ":"),
		instanceID: randomID(), lockTTL: config.LockTTL, lockWait: config.LockWait,
	}
	if coordinator.prefix == "" {
		coordinator.prefix = "flow"
	}
	if coordinator.lockTTL <= 0 {
		coordinator.lockTTL = 30 * time.Second
	}
	if coordinator.lockWait <= 0 {
		coordinator.lockWait = 5 * time.Second
	}
	timeout := config.ConnectTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var pingErr error
	for {
		pingErr = client.Ping(probeCtx).Err()
		if pingErr == nil {
			break
		}
		select {
		case <-probeCtx.Done():
			_ = client.Close()
			return nil, fmt.Errorf("connect to Redis %s: %w", mode, pingErr)
		case <-time.After(200 * time.Millisecond):
		}
	}
	return coordinator, nil
}

func newClient(config Config) (redis.UniversalClient, error) {
	mode := strings.ToLower(strings.TrimSpace(config.Mode))
	poolSize := config.PoolSize
	if poolSize <= 0 {
		poolSize = 40
	}
	commonTLS := (*tls.Config)(nil)
	if config.TLS {
		commonTLS = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	if mode == "standalone" {
		var options *redis.Options
		var err error
		if strings.TrimSpace(config.URL) != "" {
			options, err = redis.ParseURL(strings.TrimSpace(config.URL))
			if err != nil {
				return nil, fmt.Errorf("parse FLOW_REDIS_URL: %w", err)
			}
		} else {
			options = &redis.Options{Addr: firstAddress(config.Addrs), Username: config.Username, Password: config.Password, DB: config.DB, TLSConfig: commonTLS}
		}
		applyOptions(options, config, poolSize)
		return redis.NewClient(options), nil
	}
	if mode != "cluster" {
		return nil, fmt.Errorf("unsupported Redis mode %q", mode)
	}
	var options *redis.ClusterOptions
	var err error
	if strings.TrimSpace(config.URL) != "" {
		options, err = redis.ParseClusterURL(strings.TrimSpace(config.URL))
		if err != nil {
			return nil, fmt.Errorf("parse FLOW_REDIS_URL: %w", err)
		}
	} else {
		options = &redis.ClusterOptions{Addrs: slices.Clone(config.Addrs), Username: config.Username, Password: config.Password, TLSConfig: commonTLS}
	}
	options.PoolSize = poolSize
	options.MinIdleConns = max(0, config.MinIdleConns)
	options.DialTimeout = config.DialTimeout
	options.ReadTimeout = config.ReadTimeout
	options.WriteTimeout = config.WriteTimeout
	return redis.NewClusterClient(options), nil
}

func applyOptions(options *redis.Options, config Config, poolSize int) {
	options.PoolSize = poolSize
	options.MinIdleConns = max(0, config.MinIdleConns)
	if config.DialTimeout > 0 {
		options.DialTimeout = config.DialTimeout
	}
	if config.ReadTimeout > 0 {
		options.ReadTimeout = config.ReadTimeout
	}
	if config.WriteTimeout > 0 {
		options.WriteTimeout = config.WriteTimeout
	}
}

func firstAddress(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func (r *Redis) Close() error                   { return r.client.Close() }
func (r *Redis) Mode() string                   { return r.mode }
func (r *Redis) InstanceID() string             { return r.instanceID }
func (r *Redis) Ping(ctx context.Context) error { return r.client.Ping(ctx).Err() }

func (r *Redis) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error) {
	values, err := rateLimitScript.Run(ctx, r.client, []string{r.key("rate", hash(key))}, window.Milliseconds()).Int64Slice()
	if err != nil {
		return false, 0, err
	}
	if len(values) != 2 {
		return false, 0, errors.New("invalid Redis rate limit response")
	}
	retry := time.Duration(max(int64(0), values[1])) * time.Millisecond
	return values[0] <= int64(limit), retry, nil
}

func (r *Redis) WithWorkspaceLock(ctx context.Context, workspace string, action func() error) error {
	key := r.key("lock", slot(workspace))
	token := randomID()
	deadline := time.Now().Add(r.lockWait)
	for {
		acquired, err := r.client.SetNX(ctx, key, token, r.lockTTL).Result()
		if err != nil {
			return fmt.Errorf("acquire Redis workspace lock: %w", err)
		}
		if acquired {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("workspace %q is busy", workspace)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	done := make(chan struct{})
	go r.refreshLock(key, token, done)
	err := action()
	close(done)
	releaseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, releaseErr := unlockScript.Run(releaseCtx, r.client, []string{key}, token).Result()
	if err != nil {
		return err
	}
	if releaseErr != nil {
		return fmt.Errorf("release Redis workspace lock: %w", releaseErr)
	}
	return nil
}

// WithLeaderLock runs action on at most one connected instance. Unlike the
// workspace mutation lock it is non-blocking, so every instance may poll while
// only the elected instance performs a scheduler tick.
func (r *Redis) WithLeaderLock(ctx context.Context, name string, action func() error) (bool, error) {
	key, token := r.key("leader", hash(name)), randomID()
	acquired, err := r.client.SetNX(ctx, key, token, r.lockTTL).Result()
	if err != nil || !acquired {
		return false, err
	}
	done := make(chan struct{})
	go r.refreshLock(key, token, done)
	actionErr := action()
	close(done)
	releaseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, releaseErr := unlockScript.Run(releaseCtx, r.client, []string{key}, token).Result()
	if actionErr != nil {
		return true, actionErr
	}
	if releaseErr != nil {
		return true, fmt.Errorf("release Redis leader lock: %w", releaseErr)
	}
	return true, nil
}

func (r *Redis) refreshLock(key, token string, done <-chan struct{}) {
	interval := r.lockTTL / 3
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), interval)
			_, _ = refreshLockScript.Run(ctx, r.client, []string{key}, token, r.lockTTL.Milliseconds()).Result()
			cancel()
		}
	}
}

func (r *Redis) Publish(ctx context.Context, workspace string, event domain.RealtimeEvent) error {
	raw, err := json.Marshal(EventEnvelope{Source: r.instanceID, Workspace: workspace, Event: event})
	if err != nil {
		return err
	}
	return r.client.Publish(ctx, r.key("events", "global"), raw).Err()
}

func (r *Redis) Listen(ctx context.Context, handle func(EventEnvelope)) error {
	pubsub := r.client.Subscribe(ctx, r.key("events", "global"))
	defer pubsub.Close()
	if _, err := pubsub.Receive(ctx); err != nil {
		return err
	}
	messages := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case message, ok := <-messages:
			if !ok {
				return errors.New("Redis event subscription closed")
			}
			var envelope EventEnvelope
			if json.Unmarshal([]byte(message.Payload), &envelope) == nil && envelope.Source != r.instanceID && envelope.Workspace != "" {
				handle(envelope)
			}
		}
	}
}

func (r *Redis) UpdatePresence(ctx context.Context, workspace, clientID string, presence domain.Presence, ttl time.Duration) ([]domain.Presence, error) {
	raw, err := json.Marshal(presence)
	if err != nil {
		return nil, err
	}
	seenKey, itemsKey := r.presenceKeys(workspace)
	pipe := r.client.TxPipeline()
	pipe.ZAdd(ctx, seenKey, redis.Z{Score: float64(presence.LastSeenAt.UnixMilli()), Member: clientID})
	pipe.HSet(ctx, itemsKey, clientID, raw)
	pipe.PExpire(ctx, seenKey, ttl*2)
	pipe.PExpire(ctx, itemsKey, ttl*2)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	return r.Presence(ctx, workspace, ttl)
}

func (r *Redis) RemovePresence(ctx context.Context, workspace, clientID string, ttl time.Duration) ([]domain.Presence, error) {
	seenKey, itemsKey := r.presenceKeys(workspace)
	pipe := r.client.TxPipeline()
	pipe.ZRem(ctx, seenKey, clientID)
	pipe.HDel(ctx, itemsKey, clientID)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	return r.Presence(ctx, workspace, ttl)
}

func (r *Redis) Presence(ctx context.Context, workspace string, ttl time.Duration) ([]domain.Presence, error) {
	presence, _, err := r.presence(ctx, workspace, ttl)
	return presence, err
}

func (r *Redis) CleanupPresence(ctx context.Context, workspace string, ttl time.Duration) ([]domain.Presence, bool, error) {
	return r.presence(ctx, workspace, ttl)
}

func (r *Redis) presence(ctx context.Context, workspace string, ttl time.Duration) ([]domain.Presence, bool, error) {
	seenKey, itemsKey := r.presenceKeys(workspace)
	cutoff := time.Now().UTC().Add(-ttl).UnixMilli()
	values, err := presenceSnapshotScript.Run(ctx, r.client, []string{seenKey, itemsKey}, cutoff).Slice()
	if err != nil && err != redis.Nil {
		return nil, false, err
	}
	if len(values) == 0 {
		return []domain.Presence{}, false, nil
	}
	removed, _ := values[0].(int64)
	result := make([]domain.Presence, 0, len(values)-1)
	for _, raw := range values[1:] {
		value, ok := raw.(string)
		if !ok {
			continue
		}
		var presence domain.Presence
		if json.Unmarshal([]byte(value), &presence) == nil {
			result = append(result, presence)
		}
	}
	slices.SortFunc(result, func(left, right domain.Presence) int { return left.LastSeenAt.Compare(right.LastSeenAt) })
	return result, removed > 0, nil
}

func (r *Redis) presenceKeys(workspace string) (string, string) {
	tag := slot(workspace)
	return r.key("presence-seen", tag), r.key("presence-items", tag)
}

func (r *Redis) key(kind, suffix string) string { return r.prefix + ":" + kind + ":" + suffix }
func slot(value string) string                  { return "{" + hash(value)[:16] + "}" }
func hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func randomID() string {
	value := make([]byte, 16)
	_, _ = rand.Read(value)
	return hex.EncodeToString(value)
}
