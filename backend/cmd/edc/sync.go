package main

import (
	"fmt"
	"path/filepath"

	"event-driven-context/internal/localcollection"
)

type collectionSyncResult struct {
	ProjectID string                     `json:"project_id"`
	Output    string                     `json:"output"`
	Notes     notesSyncResult            `json:"notes"`
	Files     localcollection.SyncResult `json:"files"`
}

func (a *app) sync(args []string) error {
	f := a.flags("sync")
	project := f.String("project", "", "project ID (defaults to directory binding)")
	output := f.String("output", "", "local collection directory")
	files := f.String("files", "metadata", "metadata (default) or all attachment bytes")
	if err := parse(f, args); err != nil {
		return err
	}
	if *output == "" {
		return fmt.Errorf("--output is required")
	}
	if *files != "metadata" && *files != "all" {
		return fmt.Errorf("--files must be metadata or all")
	}
	if err := a.requiredProject(project); err != nil {
		return err
	}
	result, err := a.syncCollection(*project, *output, *files == "all")
	if err != nil {
		// Completion fields remain false for interrupted phases; already verified
		// files stay available and a later run can resume.
		if result.Output != "" {
			if encodeErr := a.json(result); encodeErr != nil {
				return encodeErr
			}
		}
		return err
	}
	return a.json(result)
}
func (a *app) syncCollection(project, output string, all bool) (result collectionSyncResult, err error) {
	absolute, err := filepath.Abs(output)
	if err != nil {
		return result, err
	}
	collection, err := localcollection.Open(absolute, a.client.BaseURL, project)
	if err != nil {
		return result, err
	}
	defer collection.Close()
	result = collectionSyncResult{ProjectID: project, Output: absolute}
	defer func() { result.Files.Manifest = collection.Manifest() }()
	notesDir, err := collection.NotesDirectory()
	if err != nil {
		return result, err
	}
	if err = collection.BeginNotes(); err != nil {
		return result, err
	}
	result.Notes, err = a.syncNotes(project, notesDir)
	if err != nil {
		return result, err
	}
	if err = collection.CompleteNotes(result.Notes.Revision, result.Notes.ThroughSequence); err != nil {
		return result, err
	}
	result.Files, err = collection.SyncFiles(a.ctx, a.client, all)
	return
}
