package service

import (
	"bricklink/parser/internal/client"
	"bricklink/parser/internal/domain"
	"bricklink/parser/internal/domain/task"
	"bricklink/parser/internal/queue"
	"bricklink/parser/internal/repository"
	"bricklink/parser/internal/state"
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
)

type Service struct {
	repository      repository.PartRepository
	client          client.BrickLinkClient
	queue           queue.Queue
	stateManager    state.StateManager
	minSaveInterval int
	groupName       string
	minIdleTime     time.Duration
}

func NewService(
	repository repository.PartRepository,
	client client.BrickLinkClient,
	queue queue.Queue,
	stateManager state.StateManager,
	minSaveInterval int,
	groupName string,
	minIdleTime int,
) *Service {
	return &Service{
		repository:      repository,
		client:          client,
		queue:           queue,
		stateManager:    stateManager,
		minSaveInterval: minSaveInterval,
		groupName:       groupName,
		minIdleTime:     time.Duration(minIdleTime) * time.Second,
	}
}

func (s *Service) ParseAll(ctx context.Context) error {
	resultsChan := make(chan *domain.CatalogResults, len(domain.CategoryTypes))
	allResults := make([]*domain.CatalogResults, 0, len(domain.CategoryTypes))

	errGroup := new(errgroup.Group)

	for _, categoryType := range domain.CategoryTypes {
		errGroup.Go(func() error {
			lastProcessedPage, err := s.stateManager.GetLastProcessedPage(ctx, categoryType)
			if err != nil {
				log.Errorf("Failed to get last processed page: %v", err)
				return err
			}

			if lastProcessedPage == 0 {
				lastProcessedPage = 1
			}

			if lastProcessedPage != 1 {
				log.Infof("🔄 Continue from page %d for %s", lastProcessedPage, categoryType.GetCategoryName())
			}

			log.Infof("🔄 Processing category: %s (%s)", categoryType.GetCategoryName(), categoryType.String())

			results, dataCh, err := s.client.GetAllCatalogPagesCh(ctx, categoryType, lastProcessedPage)
			if err != nil {
				log.Errorf("❌ Failed to get catalog pages for %s: %v", categoryType.String(), err)
				return err
			}

			countPages := 0
			for page := range dataCh {
				countPages++

				if countPages%(s.minSaveInterval) == 0 {
					s.stateManager.SetLastProcessedPage(ctx, categoryType, max(0, page.PageNumber-s.minSaveInterval))
				}

				_, err := s.queue.AddTask(ctx, &task.CatalogPageTask{
					PageNumber:   page.PageNumber,
					CategoryType: page.CategoryType,
					Items:        page.Items,
				})
				if err != nil {
					log.Errorf("❌ Failed to add task for %s: %v", categoryType.String(), err)
					return err
				}
			}

			resultsChan <- results
			log.Infof("✅ Completed %s: %d pages, %d total items",
				categoryType.GetCategoryName(), len(results.Pages), results.TotalItems)

			s.stateManager.SetLastProcessedPage(ctx, categoryType, results.TotalPages)

			return nil
		})
	}

	if err := errGroup.Wait(); err != nil {
		return err
	}

	close(resultsChan)
	for results := range resultsChan {
		allResults = append(allResults, results)
	}

	log.Infof("✅ Completed all remaining categories")

	return nil
}

func (s *Service) RunWorkers(ctx context.Context, numWorkers int) error {
	var wg sync.WaitGroup

	// Run workers for both regular and retry tasks
	s.runWorkersForStream(ctx, &wg, numWorkers, "bricklink:stream:CatalogPageTask", "main")
	s.runWorkersForStream(ctx, &wg, numWorkers/2, "bricklink:stream:PageRetryTask", "retry")

	wg.Wait()
	return nil
}

func (s *Service) runWorkersForStream(ctx context.Context, wg *sync.WaitGroup, numWorkers int, streamName, workerType string) {
	// Auto-claimer for this stream
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(s.minIdleTime)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				consumer := fmt.Sprintf("autoclaimer-%s-%d", workerType, time.Now().UnixNano())
				claimedMessages, err := s.queue.AutoClaim(ctx, s.groupName, consumer, streamName, s.minIdleTime)
				if err != nil {
					log.Errorf("❌ Failed to auto-claim messages for %s: %v", streamName, err)
					continue
				}
				if len(claimedMessages) > 0 {
					log.Infof("🔄 Auto-claimed %d messages from %s stream", len(claimedMessages), workerType)
					for _, msg := range claimedMessages {
						err := s.processMessage(ctx, &msg)
						if err != nil {
							log.Errorf("❌ Failed to process auto-claimed message %s: %v", msg.ID, err)
						}
					}
				}
			}
		}
	}()

	// Regular workers for this stream
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			consumer := fmt.Sprintf("%s-worker-%d", workerType, workerID)
			log.Infof("🚀 Starting %s worker %d as consumer %s", workerType, workerID, consumer)
			for {
				select {
				case <-ctx.Done():
					log.Infof("🛑 %s worker %d stopping", workerType, workerID)
					return
				default:
					msg, err := s.queue.GetTask(ctx, s.groupName, consumer, streamName)
					if err != nil {
						log.Errorf("❌ Failed to get task from %s: %v", streamName, err)
						continue
					}

					if msg != nil {
						err := s.processMessage(ctx, msg)
						if err != nil {
							log.Errorf("❌ Failed to process message %s: %v", msg.ID, err)
						}
					}
				}
			}
		}(i + 1)
	}
}

func (s *Service) processMessage(ctx context.Context, msg *redis.XMessage) error {
	taskType, ok := msg.Values["task_type"].(string)
	if !ok {
		return fmt.Errorf("invalid task type in message %s", msg.ID)
	}

	taskData, ok := msg.Values["task_data"].(string)
	if !ok {
		return fmt.Errorf("invalid task data in message %s", msg.ID)
	}

	var streamName string
	switch taskType {
	case "CatalogPageTask":
		streamName = "bricklink:stream:CatalogPageTask"
		pageTask, err := task.UnmarshalTask[*task.CatalogPageTask]([]byte(taskData))
		if err != nil {
			return fmt.Errorf("failed to unmarshal catalog page task data: %w", err)
		}

		if err := s.parsePage(ctx, pageTask); err != nil {
			// Add to retry queue instead of failing completely
			retryTask := &task.PageRetryTask{
				PageNumber:   pageTask.PageNumber,
				CategoryType: pageTask.CategoryType,
				RetryCount:   0,
				Error:        err.Error(),
			}

			if _, addErr := s.queue.AddTask(ctx, retryTask); addErr != nil {
				log.Errorf("❌ Failed to add retry task for page %d: %v", pageTask.PageNumber, addErr)
			} else {
				log.Warnf("🔄 Added page %d to retry queue due to error: %v", pageTask.PageNumber, err)
			}
		}

	case "PageRetryTask":
		streamName = "bricklink:stream:PageRetryTask"
		retryTask, err := task.UnmarshalTask[*task.PageRetryTask]([]byte(taskData))
		if err != nil {
			return fmt.Errorf("failed to unmarshal retry task data: %w", err)
		}

		if err := s.retryPage(ctx, retryTask); err != nil {
			return fmt.Errorf("failed to retry page: %w", err)
		}

	default:
		return fmt.Errorf("unknown task type: %s", taskType)
	}

	if err := s.queue.AckTask(ctx, streamName, s.groupName, msg.ID); err != nil {
		return fmt.Errorf("failed to ack message %s: %w", msg.ID, err)
	}

	return nil
}

func (s *Service) parsePage(ctx context.Context, pageTask *task.CatalogPageTask) error {
	for _, item := range pageTask.Items {
		itemType, itemID, err := s.parseItemURL(item.ItemURL)
		if err != nil {
			log.Errorf("❌ Failed to parse URL %s: %v", item.ItemURL, err)
			continue
		}

		details, err := s.client.GetItemDetails(ctx, itemType, itemID)
		if err != nil {
			log.Errorf("❌ Failed to get item details for %s: %v", item.ItemURL, err)
			continue
		}

		err = s.repository.SavePartDetails(ctx, itemType, details)
		if err != nil {
			log.Errorf("❌ Failed to save part details for %s: %v", details.ItemNumber, err)
			continue
		}
	}

	return nil
}

func (s *Service) retryPage(ctx context.Context, retryTask *task.PageRetryTask) error {
	// Increment retry count
	retryTask.RetryCount++

	log.Infof("🔄 Retrying page %d for %s (attempt %d)",
		retryTask.PageNumber, retryTask.CategoryType, retryTask.RetryCount)

	// Try to fetch the page again
	page, err := s.client.GetCatalogPage(ctx, retryTask.CategoryType, retryTask.PageNumber)
	if err != nil {
		// Create new retry task with incremented count - retry indefinitely
		newRetryTask := &task.PageRetryTask{
			PageNumber:   retryTask.PageNumber,
			CategoryType: retryTask.CategoryType,
			RetryCount:   retryTask.RetryCount,
			Error:        err.Error(),
		}

		if _, addErr := s.queue.AddTask(ctx, newRetryTask); addErr != nil {
			log.Errorf("❌ Failed to re-add retry task for page %d: %v", retryTask.PageNumber, addErr)
			return addErr
		}

		log.Warnf("🔄 Page %d for %s failed again, will retry (attempt %d): %v",
			retryTask.PageNumber, retryTask.CategoryType, retryTask.RetryCount, err)
		return nil
	}

	// Success! Create a regular CatalogPageTask to process the items
	pageTask := &task.CatalogPageTask{
		PageNumber:   page.PageNumber,
		CategoryType: page.CategoryType,
		Items:        page.Items,
	}

	if _, err := s.queue.AddTask(ctx, pageTask); err != nil {
		log.Errorf("❌ Failed to add recovered page task for page %d: %v", retryTask.PageNumber, err)
		return err
	}

	log.Infof("✅ Successfully recovered page %d for %s after %d attempts",
		retryTask.PageNumber, retryTask.CategoryType, retryTask.RetryCount)
	return nil
}

func (s *Service) parseItemURL(url string) (domain.CategoryType, string, error) {
	// Extract item type and ID from URLs like: /v2/catalog/catalogitem.page?P=11293pb007
	regex := regexp.MustCompile(`[?&]([SPMGBspgmb])=([^&]+)`)
	matches := regex.FindStringSubmatch(url)
	if len(matches) < 3 {
		return "", "", fmt.Errorf("could not extract item type and ID from URL: %s", url)
	}

	return domain.CategoryType(strings.ToUpper(matches[1])), matches[2], nil
}
