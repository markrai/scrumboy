package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const rankStep int64 = 1000

type neighbor struct {
	id   int64
	rank int64
}

func computeNewRank(ctx context.Context, tx *sql.Tx, projectID int64, columnKey string, movingID *int64, afterID, beforeID *int64) (int64, error) {
	var after, before *neighbor
	if afterID != nil {
		n, err := getNeighbor(ctx, tx, projectID, columnKey, *afterID)
		if err != nil {
			return 0, err
		}
		after = &n
	}
	if beforeID != nil {
		n, err := getNeighbor(ctx, tx, projectID, columnKey, *beforeID)
		if err != nil {
			return 0, err
		}
		before = &n
	}

	if after != nil && before != nil {
		if !lessByRankID(after.rank, after.id, before.rank, before.id) {
			return 0, fmt.Errorf("%w: afterId must come before beforeId", ErrConflict)
		}
	}

	if movingID != nil {
		if after != nil && after.id == *movingID {
			return 0, fmt.Errorf("%w: invalid afterId", ErrValidation)
		}
		if before != nil && before.id == *movingID {
			return 0, fmt.Errorf("%w: invalid beforeId", ErrValidation)
		}
	}

	switch {
	case after == nil && before == nil:
		maxRank, err := maxRankInColumn(ctx, tx, projectID, columnKey)
		if err != nil {
			return 0, err
		}
		return maxRank + rankStep, nil
	case after == nil && before != nil:
		if before.rank > rankStep {
			return before.rank - rankStep, nil
		}
		if err := rebalanceColumn(ctx, tx, projectID, columnKey); err != nil {
			return 0, err
		}
		before2, err := getNeighbor(ctx, tx, projectID, columnKey, before.id)
		if err != nil {
			return 0, err
		}
		if before2.rank > rankStep {
			return before2.rank - rankStep, nil
		}
		return before2.rank / 2, nil
	case after != nil && before == nil:
		return after.rank + rankStep, nil
	}

	gap := before.rank - after.rank
	if gap >= 2 {
		return after.rank + (gap / 2), nil
	}

	if err := rebalanceColumn(ctx, tx, projectID, columnKey); err != nil {
		return 0, err
	}

	after2, err := getNeighbor(ctx, tx, projectID, columnKey, after.id)
	if err != nil {
		return 0, err
	}
	before2, err := getNeighbor(ctx, tx, projectID, columnKey, before.id)
	if err != nil {
		return 0, err
	}
	if !lessByRankID(after2.rank, after2.id, before2.rank, before2.id) {
		return 0, fmt.Errorf("%w: afterId must come before beforeId", ErrConflict)
	}
	gap = before2.rank - after2.rank
	if gap < 2 {
		return 0, fmt.Errorf("%w: cannot compute rank", ErrConflict)
	}
	return after2.rank + (gap / 2), nil
}

func getNeighbor(ctx context.Context, tx *sql.Tx, projectID int64, columnKey string, id int64) (neighbor, error) {
	row := tx.QueryRowContext(ctx, `SELECT id, rank FROM todos WHERE id=? AND project_id=? AND column_key=?`, id, projectID, columnKey)
	var n neighbor
	if err := row.Scan(&n.id, &n.rank); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return neighbor{}, ErrNotFound
		}
		return neighbor{}, fmt.Errorf("get neighbor: %w", err)
	}
	return n, nil
}

func maxRankInColumn(ctx context.Context, tx *sql.Tx, projectID int64, columnKey string) (int64, error) {
	row := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(rank), 0) FROM todos WHERE project_id=? AND column_key=?`, projectID, columnKey)
	var max int64
	if err := row.Scan(&max); err != nil {
		return 0, fmt.Errorf("max rank: %w", err)
	}
	return max, nil
}

func rebalanceColumn(ctx context.Context, tx *sql.Tx, projectID int64, columnKey string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM todos WHERE project_id=? AND column_key=? ORDER BY rank ASC, id ASC`, projectID, columnKey)
	if err != nil {
		return fmt.Errorf("rebalance list: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("scan rebalance id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows rebalance list: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `UPDATE todos SET rank=? WHERE id=?`)
	if err != nil {
		return fmt.Errorf("prepare rebalance update: %w", err)
	}
	defer stmt.Close()

	for i, id := range ids {
		newRank := int64(i+1) * rankStep
		if _, err := stmt.ExecContext(ctx, newRank, id); err != nil {
			return fmt.Errorf("rebalance update: %w", err)
		}
	}
	return nil
}

func lessByRankID(rankA, idA, rankB, idB int64) bool {
	if rankA < rankB {
		return true
	}
	if rankA > rankB {
		return false
	}
	return idA < idB
}
