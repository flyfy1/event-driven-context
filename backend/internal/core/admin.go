package core

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// AdminUser is the identity summary exposed only through the administrator API.
type AdminUser struct {
	User
	ProjectCount      int `json:"project_count"`
	OwnedProjectCount int `json:"owned_project_count"`
}

type AdminProjectMember struct {
	User
	Owner bool `json:"owner"`
}

type AdminProject struct {
	Project
	Owner   User                 `json:"owner"`
	Members []AdminProjectMember `json:"members"`
}

type AdminIdentitySnapshot struct {
	Users    []AdminUser    `json:"users"`
	Projects []AdminProject `json:"projects"`
}

// RequireAdmin checks a deployment-owned allowlist against the authenticated
// user's immutable ID, username, or verified email. An empty list disables the
// administrator surface.
func (s *Store) RequireAdmin(ctx context.Context, allowed []string) error {
	user, err := s.Me(ctx)
	if err != nil {
		return err
	}
	for _, candidate := range allowed {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" && (candidate == user.ID || strings.EqualFold(candidate, user.Username) || strings.EqualFold(candidate, user.Email)) {
			return nil
		}
	}
	return &Error{Code: "forbidden", Message: "administrator access required"}
}

func (s *Store) AdminIdentitySnapshot(ctx context.Context, allowed []string) (AdminIdentitySnapshot, error) {
	out := AdminIdentitySnapshot{Users: []AdminUser{}, Projects: []AdminProject{}}
	if err := s.RequireAdmin(ctx, allowed); err != nil {
		return out, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id,u.username,COALESCE(u.email,''),u.created_at,
		       (SELECT count(*) FROM members m WHERE m.user_id=u.id),
		       (SELECT count(*) FROM projects p WHERE p.owner_user_id=u.id)
		FROM users u ORDER BY u.created_at,u.id`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var user AdminUser
		if err = rows.Scan(&user.ID, &user.Username, &user.Email, &user.CreatedAt, &user.ProjectCount, &user.OwnedProjectCount); err != nil {
			_ = rows.Close()
			return out, err
		}
		out.Users = append(out.Users, user)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return out, err
	}
	if err = rows.Close(); err != nil {
		return out, err
	}

	rows, err = s.db.QueryContext(ctx, `
		SELECT p.id,p.name,p.description,p.timezone,p.owner_user_id,p.created_at,
		       u.id,u.username,COALESCE(u.email,''),u.created_at
		FROM projects p JOIN users u ON u.id=p.owner_user_id
		ORDER BY p.created_at,p.id`)
	if err != nil {
		return out, err
	}
	projectIndex := map[string]int{}
	for rows.Next() {
		var project AdminProject
		project.Members = []AdminProjectMember{}
		if err = rows.Scan(
			&project.ID, &project.Name, &project.Description, &project.Timezone, &project.OwnerUserID, &project.CreatedAt,
			&project.Owner.ID, &project.Owner.Username, &project.Owner.Email, &project.Owner.CreatedAt,
		); err != nil {
			_ = rows.Close()
			return out, err
		}
		projectIndex[project.ID] = len(out.Projects)
		out.Projects = append(out.Projects, project)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return out, err
	}
	if err = rows.Close(); err != nil {
		return out, err
	}

	rows, err = s.db.QueryContext(ctx, `
		SELECT m.project_id,u.id,u.username,COALESCE(u.email,''),u.created_at,p.owner_user_id=u.id
		FROM members m JOIN users u ON u.id=m.user_id JOIN projects p ON p.id=m.project_id
		ORDER BY m.project_id,(p.owner_user_id=u.id) DESC,u.username,u.id`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var projectID string
		var member AdminProjectMember
		if err = rows.Scan(&projectID, &member.ID, &member.Username, &member.Email, &member.CreatedAt, &member.Owner); err != nil {
			return out, err
		}
		if index, ok := projectIndex[projectID]; ok {
			out.Projects[index].Members = append(out.Projects[index].Members, member)
		}
	}
	return out, rows.Err()
}

// SetAdminProjectMember grants or removes the project's existing read/write
// membership. Ownership itself is deliberately not mutable through this MVP.
func (s *Store) SetAdminProjectMember(ctx context.Context, allowed []string, projectID, userID string, access bool) (AdminProjectMember, error) {
	if err := s.RequireAdmin(ctx, allowed); err != nil {
		return AdminProjectMember{}, err
	}
	projectID = strings.TrimSpace(projectID)
	userID = strings.TrimSpace(userID)
	var ownerID string
	if err := s.db.QueryRowContext(ctx, "SELECT owner_user_id FROM projects WHERE id=?", projectID).Scan(&ownerID); errors.Is(err, sql.ErrNoRows) {
		return AdminProjectMember{}, ErrNotFound
	} else if err != nil {
		return AdminProjectMember{}, err
	}
	var member AdminProjectMember
	if err := s.db.QueryRowContext(ctx, "SELECT id,username,COALESCE(email,''),created_at FROM users WHERE id=?", userID).Scan(&member.ID, &member.Username, &member.Email, &member.CreatedAt); errors.Is(err, sql.ErrNoRows) {
		return AdminProjectMember{}, ErrNotFound
	} else if err != nil {
		return AdminProjectMember{}, err
	}
	member.Owner = userID == ownerID
	if member.Owner && !access {
		return AdminProjectMember{}, Invalid("project owner access cannot be removed")
	}
	var err error
	if access {
		_, err = s.db.ExecContext(ctx, "INSERT INTO members(project_id,user_id) VALUES(?,?) ON CONFLICT DO NOTHING", projectID, userID)
	} else {
		_, err = s.db.ExecContext(ctx, "DELETE FROM members WHERE project_id=? AND user_id=?", projectID, userID)
	}
	return member, err
}
