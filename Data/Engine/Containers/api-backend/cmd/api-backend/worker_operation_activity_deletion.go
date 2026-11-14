package main

import (
	"context"
	"database/sql"
	"errors"

	"github.com/lib/pq"
)

var errWorkerOperationActivityRetained = errors.New("activity retains unresolved site-worker execution; deletion requires terminal receipt")

// Lock the exact deletion set, in the same activity-ID order for every caller.
// Admission also locks its activity before publishing the durable charge. A
// dispatch acknowledgement, timeout or ordinary status edit cannot erase that
// binding. Reject the whole caller transaction; never report a partial delete.
func guardWorkerOperationActivityDeletion(ctx context.Context, tx *sql.Tx, ids []int64) error {
	ids = uniquePositiveInt64s(ids)
	if len(ids) == 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,COALESCE(metadata_json,'')
		FROM engine.activity_history WHERE id=ANY($1) ORDER BY id FOR UPDATE`, pq.Array(ids))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var metadata string
		if err := rows.Scan(&id, &metadata); err != nil {
			return err
		}
		if !workerOperationActivityDeletionAllowed(id, metadata) {
			return errWorkerOperationActivityRetained
		}
	}
	return rows.Err()
}
