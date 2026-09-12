package core

import "context"

// NotesTargets is server-internal discovery. Never expose it through user or MCP routes.
type NotesTarget struct{ ProjectID, OwnerID string }

func (s *Store) NotesTargets(ctx context.Context) ([]NotesTarget, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT project_id,MIN(user_id) FROM project_owners GROUP BY project_id ORDER BY project_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NotesTarget{}
	for rows.Next() {
		var t NotesTarget
		if err = rows.Scan(&t.ProjectID, &t.OwnerID); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
