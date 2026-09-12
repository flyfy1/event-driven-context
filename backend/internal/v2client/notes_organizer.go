package v2client

import (
	"context"
	"event-driven-context/internal/notes"
	"net/http"
)

func (c *Client) BeginNotesOrganization(ctx context.Context, projectID, version string) (out notes.OrganizationRun, err error) {
	err = c.doJSON(ctx, http.MethodPost, projectPath(projectID, "/notes/organizer/begin"), notes.BeginInput{PromptVersion: version}, &out)
	return
}
func (c *Client) PublishNotesOrganization(ctx context.Context, projectID string, in notes.PublishInput) (out notes.PublishResult, err error) {
	err = c.doJSON(ctx, http.MethodPost, projectPath(projectID, "/notes/organizer/publish"), in, &out)
	return
}
func (c *Client) CancelNotesOrganization(ctx context.Context, projectID, runID string) error {
	return c.doJSON(ctx, http.MethodPost, projectPath(projectID, "/notes/organizer/cancel"), map[string]string{"run_id": runID}, nil)
}
