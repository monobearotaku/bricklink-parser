package queue

import (
	"bricklink/parser/internal/config"
	"bricklink/parser/internal/domain/task"
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
)

type Queue interface {
	AddTask(ctx context.Context, task task.Task) (string, error) // Returns message ID
	GetTask(ctx context.Context, group, consumer, stream string) (*redis.XMessage, error)
	AckTask(ctx context.Context, stream, group, msgID string) error
	CreateGroup(ctx context.Context, stream, group string) error
	AutoClaim(ctx context.Context, group, consumer, stream string, minIdleTime time.Duration) ([]redis.XMessage, error)
	EnsureStreamsExist(ctx context.Context) error
}

type RedisQueue struct {
	redisClient  *redis.Client
	streamPrefix string
	groupName    string
}

func NewRedisQueue(redisClient *redis.Client, cfg config.RedisConfig) (Queue, error) {
	q := &RedisQueue{
		redisClient:  redisClient,
		streamPrefix: "bricklink:stream:",
		groupName:    cfg.ConsumerGroup,
	}

	// Ensure all streams and consumer groups exist before workers start
	ctx := context.Background()
	err := q.EnsureStreamsExist(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure streams exist: %w", err)
	}

	return q, nil
}

func (q *RedisQueue) CreateGroup(ctx context.Context, stream, group string) error {
	err := q.redisClient.XGroupCreateMkStream(ctx, stream, group, "0").Err()
	if err != nil && err.Error() == "BUSYGROUP Consumer Group name already exists" {
		log.Infof("Group %s already exists for stream %s", group, stream)
		return nil
	}
	return err
}

func (q *RedisQueue) AddTask(ctx context.Context, task task.Task) (string, error) {
	// Get task type to determine stream name
	taskType := task.TaskType()
	streamName := q.streamPrefix + taskType

	// Serialize task to JSON
	taskValue, err := task.TaskValue()
	if err != nil {
		return "", fmt.Errorf("failed to serialize task: %w", err)
	}

	// Add task to Redis stream using XADD
	// Fields: task_type, task_data
	messageID, err := q.redisClient.XAdd(ctx, &redis.XAddArgs{
		Stream: streamName,
		Values: map[string]interface{}{
			"task_type": taskType,
			"task_data": string(taskValue),
		},
	}).Result()

	if err != nil {
		return "", fmt.Errorf("failed to add task to Redis stream %s: %w", streamName, err)
	}

	log.Debugf("Added task %s to stream %s with message ID: %s", taskType, streamName, messageID)
	return messageID, nil
}

func (q *RedisQueue) GetTask(ctx context.Context, group, consumer, stream string) (*redis.XMessage, error) {
	result, err := q.redisClient.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    group,
		Consumer: consumer,
		Streams:  []string{stream, ">"},
		Count:    1,
		Block:    5 * time.Second,
	}).Result()

	if err != nil {
		if err == redis.Nil {
			return nil, nil // No new messages
		}
		return nil, fmt.Errorf("failed to read from Redis stream %s: %w", stream, err)
	}

	if len(result) == 0 || len(result[0].Messages) == 0 {
		return nil, nil // No new messages
	}

	return &result[0].Messages[0], nil
}

func (q *RedisQueue) AckTask(ctx context.Context, stream, group, msgID string) error {
	return q.redisClient.XAck(ctx, stream, group, msgID).Err()
}

func (q *RedisQueue) AutoClaim(
	ctx context.Context,
	group,
	consumer,
	stream string,
	minIdleTime time.Duration,
) ([]redis.XMessage, error) {
	result, _, err := q.redisClient.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   stream,
		Group:    group,
		Consumer: consumer,
		MinIdle:  minIdleTime,
		Start:    "0-0",
		Count:    1,
	}).Result()

	if err != nil && err != redis.Nil {
		return nil, fmt.Errorf("failed to claim messages from Redis stream %s: %w", stream, err)
	}

	return result, nil
}

func (q *RedisQueue) Close() error {
	if q.redisClient != nil {
		return q.redisClient.Close()
	}
	return nil
}

// GetRedisClient returns the underlying Redis client for use by other components
func (q *RedisQueue) GetRedisClient() *redis.Client {
	return q.redisClient
}

// EnsureStreamsExist creates all required streams and consumer groups upfront
func (q *RedisQueue) EnsureStreamsExist(ctx context.Context) error {
	taskTypes := []string{"CatalogPageTask", "PageRetryTask", "ItemRetryTask"}

	log.Info("🔧 Creating Redis streams and consumer groups...")

	for _, taskType := range taskTypes {
		streamName := q.streamPrefix + taskType

		// First, try to create a dummy entry to ensure the stream exists
		// We'll delete this immediately after creating the consumer group
		dummyID, err := q.redisClient.XAdd(ctx, &redis.XAddArgs{
			Stream: streamName,
			Values: map[string]interface{}{
				"init": "dummy",
			},
		}).Result()

		if err != nil {
			log.Warnf("⚠️ Failed to create stream %s with dummy entry: %v", streamName, err)
		} else {
			log.Debugf("✅ Created stream %s with dummy entry %s", streamName, dummyID)
		}

		// Create the consumer group
		err = q.CreateGroup(ctx, streamName, q.groupName)
		if err != nil {
			return fmt.Errorf("failed to create consumer group for %s: %w", taskType, err)
		}

		// Remove the dummy entry if we created one
		if dummyID != "" {
			err = q.redisClient.XDel(ctx, streamName, dummyID).Err()
			if err != nil {
				log.Warnf("⚠️ Failed to delete dummy entry from %s: %v", streamName, err)
			} else {
				log.Debugf("🗑️ Removed dummy entry from %s", streamName)
			}
		}

		log.Infof("✅ Stream %s and consumer group %s ready", streamName, q.groupName)
	}

	log.Info("🎉 All Redis streams and consumer groups created successfully")
	return nil
}
