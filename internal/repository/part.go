package repository

import (
	"context"
	"fmt"

	"bricklink/parser/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PartRepository interface {
	SavePartDetails(ctx context.Context, categoryType domain.CategoryType, details *domain.PartDetails) error
}

type partRepository struct {
	db *pgxpool.Pool
}

func NewPartRepository(db *pgxpool.Pool) PartRepository {
	return &partRepository{
		db: db,
	}
}

func (r *partRepository) SavePartDetails(ctx context.Context, categoryType domain.CategoryType, details *domain.PartDetails) error {
	query := `
	INSERT INTO part_details (id, category, data) 
	VALUES ($1, $2, $3) 
	ON CONFLICT (id) 
	DO UPDATE SET category = $2, data = $3`
	_, err := r.db.Exec(ctx, query, details.ItemNumber, categoryType.String(), details)
	if err != nil {
		return fmt.Errorf("failed to save part details: %w", err)
	}

	return nil
}
