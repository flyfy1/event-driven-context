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
	Role string `json:"role"`
}

type AdminProject struct {
	Project
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
		       (SELECT count(*) FROM project_owners owners WHERE owners.user_id=u.id)
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
		SELECT p.id,p.name,p.description,p.timezone,p.owner_user_id,p.created_at
		FROM projects p ORDER BY p.created_at,p.id`)
	if err != nil {
		return out, err
	}
	projectIndex := map[string]int{}
	for rows.Next() {
		var project AdminProject
		project.Members = []AdminProjectMember{}
		project.OwnerUserIDs = []string{}
		if err = rows.Scan(&project.ID, &project.Name, &project.Description, &project.Timezone, &project.OwnerUserID, &project.CreatedAt); err != nil {
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
		SELECT m.project_id,u.id,u.username,COALESCE(u.email,''),u.created_at,
		       CASE WHEN owners.user_id IS NULL THEN 'member' ELSE 'owner' END
		FROM members m JOIN users u ON u.id=m.user_id
		LEFT JOIN project_owners owners ON owners.project_id=m.project_id AND owners.user_id=m.user_id
		ORDER BY m.project_id,(owners.user_id IS NOT NULL) DESC,u.username,u.id`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var projectID string
		var member AdminProjectMember
		if err = rows.Scan(&projectID, &member.ID, &member.Username, &member.Email, &member.CreatedAt, &member.Role); err != nil {
			return out, err
		}
		if index, ok := projectIndex[projectID]; ok {
			out.Projects[index].Members = append(out.Projects[index].Members, member)
			if member.Role == "owner" {
				out.Projects[index].OwnerUserIDs = append(out.Projects[index].OwnerUserIDs, member.ID)
			}
		}
	}
	return out, rows.Err()
}

// SetAdminProjectAccess manages the same owner/member model used by normal
// project owners while retaining the invariant that every project has an owner.
func (s *Store) SetAdminProjectAccess(ctx context.Context, allowed []string, projectID, userID, access string) (AdminProjectMember, error) {
	if err := s.RequireAdmin(ctx, allowed); err != nil {
		return AdminProjectMember{}, err
	}
	projectID = strings.TrimSpace(projectID)
	userID = strings.TrimSpace(userID)
	access = strings.ToLower(strings.TrimSpace(access))
	if access != "owner" && access != "member" && access != "none" {
		return AdminProjectMember{}, Invalid("access must be owner, member, or none")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminProjectMember{}, err
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRowContext(ctx, "SELECT 1 FROM projects WHERE id=?", projectID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return AdminProjectMember{}, ErrNotFound
	} else if err != nil {
		return AdminProjectMember{}, err
	}
	var member AdminProjectMember
	if err = tx.QueryRowContext(ctx, "SELECT id,username,COALESCE(email,''),created_at FROM users WHERE id=?", userID).Scan(&member.ID, &member.Username, &member.Email, &member.CreatedAt); errors.Is(err, sql.ErrNoRows) {
		return AdminProjectMember{}, ErrNotFound
	} else if err != nil {
		return AdminProjectMember{}, err
	}
	currentRole := "none"
	err = tx.QueryRowContext(ctx, `
		SELECT CASE WHEN owners.user_id IS NULL THEN 'member' ELSE 'owner' END
		FROM members m LEFT JOIN project_owners owners ON owners.project_id=m.project_id AND owners.user_id=m.user_id
		WHERE m.project_id=? AND m.user_id=?`, projectID, userID).Scan(&currentRole)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return AdminProjectMember{}, err
	}
	if currentRole == "owner" && access != "owner" {
		var ownerCount int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM project_owners WHERE project_id=?", projectID).Scan(&ownerCount); err != nil {
			return AdminProjectMember{}, err
		}
		if ownerCount <= 1 {
			return AdminProjectMember{}, ErrLastOwner
		}
	}
	switch access {
	case "owner":
		if _, err = tx.ExecContext(ctx, "INSERT INTO members(project_id,user_id) VALUES(?,?) ON CONFLICT DO NOTHING", projectID, userID); err == nil {
			_, err = tx.ExecContext(ctx, "INSERT INTO project_owners(project_id,user_id) VALUES(?,?) ON CONFLICT DO NOTHING", projectID, userID)
		}
	case "member":
		if _, err = tx.ExecContext(ctx, "INSERT INTO members(project_id,user_id) VALUES(?,?) ON CONFLICT DO NOTHING", projectID, userID); err == nil {
			_, err = tx.ExecContext(ctx, "DELETE FROM project_owners WHERE project_id=? AND user_id=?", projectID, userID)
		}
	case "none":
		if _, err = tx.ExecContext(ctx, "DELETE FROM project_owners WHERE project_id=? AND user_id=?", projectID, userID); err == nil {
			_, err = tx.ExecContext(ctx, "DELETE FROM members WHERE project_id=? AND user_id=?", projectID, userID)
		}
	}
	if err != nil {
		return AdminProjectMember{}, err
	}
	member.Role = access
	if err = tx.Commit(); err != nil {
		return AdminProjectMember{}, err
	}
	return member, nil
}
