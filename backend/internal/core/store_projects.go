package core

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

func normalizeProjectTimezone(value string, defaultUTC bool) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" && defaultUTC {
		return "UTC", nil
	}
	if value == "" || value == "Local" || len(value) > 255 || !utf8.ValidString(value) {
		return "", Invalid("timezone must be a valid IANA timezone")
	}
	if _, err := time.LoadLocation(value); err != nil {
		return "", Invalid("timezone must be a valid IANA timezone")
	}
	return value, nil
}

func (s *Store) requireMember(ctx context.Context, pid string) error {
	if UserID(ctx) == "" {
		return ErrUnauthenticated
	}
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT 1 FROM members WHERE project_id=? AND user_id=?", pid, UserID(ctx)).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// RequireProjectMember exposes the existing identity boundary to sibling
// storage packages without exposing SQLite or duplicating membership queries.
func (s *Store) RequireProjectMember(ctx context.Context, projectID string) error {
	return s.requireMember(ctx, projectID)
}

// RequireProjectOwner is the narrow management check used by project-scoped
// extensions such as plugin installation.
func (s *Store) RequireProjectOwner(ctx context.Context, projectID string) error {
	if err := s.requireMember(ctx, projectID); err != nil {
		return err
	}
	var owner int
	if err := s.db.QueryRowContext(ctx, "SELECT 1 FROM project_owners WHERE project_id=? AND user_id=?", projectID, UserID(ctx)).Scan(&owner); errors.Is(err, sql.ErrNoRows) {
		return ErrForbidden
	} else if err != nil {
		return err
	}
	return nil
}
func (s *Store) CreateProject(ctx context.Context, in ProjectInput) (Project, error) {
	if UserID(ctx) == "" {
		return Project{}, ErrUnauthenticated
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 200 || len(in.Description) > 4000 {
		return Project{}, Invalid("name required (max 200 bytes); description max 4000 bytes")
	}
	timezone, err := normalizeProjectTimezone(in.Timezone, true)
	if err != nil {
		return Project{}, err
	}
	p := Project{ID: newID("prj"), Name: in.Name, Description: in.Description, Timezone: timezone, OwnerUserID: UserID(ctx), OwnerUserIDs: []string{UserID(ctx)}, CreatedAt: now()}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Project{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "INSERT INTO projects(id,name,description,timezone,owner_user_id,created_at) VALUES(?,?,?,?,?,?)", p.ID, p.Name, p.Description, p.Timezone, p.OwnerUserID, p.CreatedAt); err != nil {
		return Project{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO members VALUES(?,?)", p.ID, p.OwnerUserID); err != nil {
		return Project{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO project_owners(project_id,user_id) VALUES(?,?)", p.ID, p.OwnerUserID); err != nil {
		return Project{}, err
	}
	return p, tx.Commit()
}
func (s *Store) ListProjects(ctx context.Context, _ Empty) (Projects, error) {
	out := Projects{Projects: []Project{}}
	if UserID(ctx) == "" {
		return out, ErrUnauthenticated
	}
	rows, err := s.db.QueryContext(ctx, "SELECT p.id,p.name,p.description,p.timezone,p.owner_user_id,p.created_at FROM projects p JOIN members m ON m.project_id=p.id WHERE m.user_id=? ORDER BY p.created_at,p.id", UserID(ctx))
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var p Project
		if err = rows.Scan(&p.ID, &p.Name, &p.Description, &p.Timezone, &p.OwnerUserID, &p.CreatedAt); err != nil {
			return out, err
		}
		out.Projects = append(out.Projects, p)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	for i := range out.Projects {
		if err = s.populateProjectOwners(ctx, &out.Projects[i]); err != nil {
			return out, err
		}
	}
	return out, nil
}

func (s *Store) UpdateProject(ctx context.Context, projectID string, in ProjectUpdateInput) (Project, error) {
	if err := s.RequireProjectOwner(ctx, projectID); err != nil {
		return Project{}, err
	}
	if in.Name == nil && in.Timezone == nil {
		return Project{}, Invalid("name or timezone required")
	}
	project, err := s.projectByID(ctx, projectID)
	if err != nil {
		return Project{}, err
	}
	if in.Name != nil {
		project.Name = strings.TrimSpace(*in.Name)
		if project.Name == "" || len(project.Name) > 200 {
			return Project{}, Invalid("name required (max 200 bytes)")
		}
	}
	if in.Timezone != nil {
		project.Timezone, err = normalizeProjectTimezone(*in.Timezone, false)
		if err != nil {
			return Project{}, err
		}
	}
	if _, err = s.db.ExecContext(ctx, "UPDATE projects SET name=?,timezone=? WHERE id=?", project.Name, project.Timezone, projectID); err != nil {
		return Project{}, err
	}
	return s.projectByID(ctx, projectID)
}

func (s *Store) UpdateProjectTimezone(ctx context.Context, projectID, timezone string) (Project, error) {
	return s.UpdateProject(ctx, projectID, ProjectUpdateInput{Timezone: &timezone})
}

func (s *Store) projectByID(ctx context.Context, projectID string) (Project, error) {
	var project Project
	err := s.db.QueryRowContext(ctx, "SELECT id,name,description,timezone,owner_user_id,created_at FROM projects WHERE id=?", projectID).Scan(
		&project.ID, &project.Name, &project.Description, &project.Timezone, &project.OwnerUserID, &project.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, err
	}
	if err = s.populateProjectOwners(ctx, &project); err != nil {
		return Project{}, err
	}
	return project, nil
}

func (s *Store) populateProjectOwners(ctx context.Context, project *Project) error {
	project.OwnerUserIDs = []string{}
	rows, err := s.db.QueryContext(ctx, "SELECT user_id FROM project_owners WHERE project_id=? ORDER BY user_id", project.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var userID string
		if err = rows.Scan(&userID); err != nil {
			return err
		}
		project.OwnerUserIDs = append(project.OwnerUserIDs, userID)
	}
	return rows.Err()
}

// ProjectTimezone is for project-scoped services that have already authorized
// their caller and need the current scheduling timezone from the identity store.
func (s *Store) ProjectTimezone(ctx context.Context, projectID string) (string, error) {
	var timezone string
	err := s.db.QueryRowContext(ctx, "SELECT timezone FROM projects WHERE id=?", projectID).Scan(&timezone)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if timezone == "" {
		return "UTC", nil
	}
	return timezone, nil
}
func (s *Store) AddMember(ctx context.Context, in MemberInput) (User, error) {
	if err := s.RequireProjectOwner(ctx, in.ProjectID); err != nil {
		return User{}, err
	}
	in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	in.Email = strings.TrimSpace(in.Email)
	if (in.Username == "") == (in.Email == "") {
		return User{}, Invalid("exactly one of username or email is required")
	}
	query := "SELECT id,username,'',created_at FROM users WHERE username=?"
	queryArg := in.Username
	if in.Email != "" {
		email, err := normalizeEmail(in.Email)
		if err != nil {
			return User{}, err
		}
		query = "SELECT id,username,'',created_at FROM users WHERE email=?"
		queryArg = email
	}
	var u User
	err := s.db.QueryRowContext(ctx, query, queryArg).Scan(&u.ID, &u.Username, &u.Email, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO members VALUES(?,?) ON CONFLICT DO NOTHING", in.ProjectID, u.ID)
	return u, err
}
func (s *Store) ListMembers(ctx context.Context, in ProjectRef) (Members, error) {
	out := Members{Members: []ProjectMember{}}
	if err := s.requireMember(ctx, in.ProjectID); err != nil {
		return out, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT u.id,u.username,'',u.created_at,CASE WHEN owners.user_id IS NULL THEN 'member' ELSE 'owner' END FROM users u JOIN members m ON m.user_id=u.id LEFT JOIN project_owners owners ON owners.project_id=m.project_id AND owners.user_id=m.user_id WHERE m.project_id=? ORDER BY u.username", in.ProjectID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var member ProjectMember
		if err = rows.Scan(&member.ID, &member.Username, &member.Email, &member.CreatedAt, &member.Role); err != nil {
			return out, err
		}
		out.Members = append(out.Members, member)
	}
	return out, rows.Err()
}

func (s *Store) SetMemberRole(ctx context.Context, in MemberRoleInput) (ProjectMember, error) {
	in.Role = strings.ToLower(strings.TrimSpace(in.Role))
	if in.Role != "member" && in.Role != "owner" {
		return ProjectMember{}, Invalid("role must be member or owner")
	}
	if err := s.RequireProjectOwner(ctx, in.ProjectID); err != nil {
		return ProjectMember{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ProjectMember{}, err
	}
	defer tx.Rollback()
	var member ProjectMember
	err = tx.QueryRowContext(ctx, "SELECT u.id,u.username,'',u.created_at,CASE WHEN owners.user_id IS NULL THEN 'member' ELSE 'owner' END FROM users u JOIN members m ON m.user_id=u.id LEFT JOIN project_owners owners ON owners.project_id=m.project_id AND owners.user_id=m.user_id WHERE m.project_id=? AND m.user_id=?", in.ProjectID, in.UserID).Scan(
		&member.ID, &member.Username, &member.Email, &member.CreatedAt, &member.Role,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectMember{}, ErrNotFound
	}
	if err != nil {
		return ProjectMember{}, err
	}
	if member.Role == "owner" && in.Role == "member" {
		var ownerCount int
		if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM project_owners WHERE project_id=?", in.ProjectID).Scan(&ownerCount); err != nil {
			return ProjectMember{}, err
		}
		if ownerCount <= 1 {
			return ProjectMember{}, ErrLastOwner
		}
	}
	if in.Role == "owner" {
		_, err = tx.ExecContext(ctx, "INSERT INTO project_owners(project_id,user_id) VALUES(?,?) ON CONFLICT DO NOTHING", in.ProjectID, in.UserID)
	} else {
		_, err = tx.ExecContext(ctx, "DELETE FROM project_owners WHERE project_id=? AND user_id=?", in.ProjectID, in.UserID)
	}
	if err != nil {
		return ProjectMember{}, err
	}
	member.Role = in.Role
	if err = tx.Commit(); err != nil {
		return ProjectMember{}, err
	}
	return member, nil
}
