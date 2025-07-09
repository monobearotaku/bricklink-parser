package state

import (
	"bricklink/parser/internal/domain"
	"context"
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"
)

type StateManager interface {
	GetLastProcessedPage(ctx context.Context, categoryType domain.CategoryType) (int, error)
	SetLastProcessedPage(ctx context.Context, categoryType domain.CategoryType, pageNumber int) error
}

type redisStateManager struct {
	redisClient *redis.Client
	keyPrefix   string
}

func NewRedisStateManager(redisClient *redis.Client) StateManager {
	return &redisStateManager{
		redisClient: redisClient,
		keyPrefix:   "bricklink:progress:page:",
	}
}

func (s *redisStateManager) GetLastProcessedPage(ctx context.Context, categoryType domain.CategoryType) (int, error) {
	key := s.keyPrefix + categoryType.String()
	val, err := s.redisClient.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return 0, nil // No progress saved yet
		}
		return 0, fmt.Errorf("failed to get last processed page for category %s: %w", categoryType, err)
	}

	page, err := strconv.Atoi(val)
	if err != nil {
		return 0, fmt.Errorf("failed to parse page number for category %s: %w", categoryType, err)
	}

	return page, nil
}

func (s *redisStateManager) SetLastProcessedPage(ctx context.Context, categoryType domain.CategoryType, pageNumber int) error {
	key := s.keyPrefix + categoryType.String()
	err := s.redisClient.Set(ctx, key, pageNumber, 0).Err() // No expiration
	if err != nil {
		return fmt.Errorf("failed to set last processed page for category %s: %w", categoryType, err)
	}
	return nil
}
