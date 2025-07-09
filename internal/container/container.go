package container

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	"bricklink/parser/internal/client"
	"bricklink/parser/internal/config"
	"bricklink/parser/internal/proxy"
	"bricklink/parser/internal/queue"
	"bricklink/parser/internal/repository"
	"bricklink/parser/internal/service"
	"bricklink/parser/internal/state"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
)

// Container holds all initialized components
type Container struct {
	Config       *config.Config
	Client       client.BrickLinkClient
	Repository   repository.PartRepository
	Queue        queue.Queue
	StateManager state.StateManager

	Service *service.Service

	db    *pgxpool.Pool
	redis *redis.Client
}

// New creates a new container with all dependencies initialized
func New(cfg *config.Config) (*Container, error) {
	container := &Container{
		Config: cfg,
	}

	// Initialize ProxySupplier
	proxySupplier, err := proxy.NewProxySupplier(context.Background(), cfg.BrickLink.Proxies, cfg.BrickLink.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize proxy supplier: %w", err)
	}

	// Initialize repository
	db, err := pgxpool.New(context.Background(),
		fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
			cfg.Database.Host,
			cfg.Database.Port,
			cfg.Database.User,
			cfg.Database.Password,
			cfg.Database.Name,
		))
	if err != nil {
		return nil, err
	}

	partRepo := repository.NewPartRepository(db)
	container.Repository = partRepo

	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Redis.Host, cfg.Redis.Port),
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.Database,
	})

	// Test connection
	_, err = rdb.Ping(context.Background()).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	log.Info("✅ Connected to Redis successfully")

	redisQueue, err := queue.NewRedisQueue(rdb, cfg.Redis)
	if err != nil {
		return nil, err
	}
	container.Queue = redisQueue

	container.redis = rdb
	stateManager := state.NewRedisStateManager(rdb)
	container.StateManager = stateManager

	// Initialize client with queue (after queue is created)
	brickLinkClient := client.NewBrickLinkClient(cfg.BrickLink, proxySupplier, redisQueue)
	container.Client = brickLinkClient

	service := service.NewService(
		partRepo,
		brickLinkClient,
		redisQueue,
		stateManager,
		cfg.BrickLink.MaxWorkers,
		cfg.Redis.ConsumerGroup,
		cfg.Redis.MinIdleTime,
	)
	container.Service = service

	return container, nil
}

// Run executes full parsing (existing behavior)
func (c *Container) Run(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)

	// Run ParseAll to enqueue tasks
	g.Go(func() error {
		return c.Service.ParseAll(ctx)
	})

	// Run workers to process tasks
	g.Go(func() error {
		return c.Service.RunWorkers(ctx, c.Config.BrickLink.MaxWorkers)
	})

	return g.Wait()
}

// Close performs cleanup when shutting down
func (c *Container) Close() error {
	log.Info("Shutting down container...")

	c.db.Close()
	c.redis.Close()

	log.Info("Container shut down successfully")
	return nil
}
