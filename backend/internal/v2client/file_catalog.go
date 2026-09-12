package v2client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"event-driven-context/internal/v2"
)

var safeFileID = regexp.MustCompile(`^file_[a-z0-9_-]{1,128}$`)

func ValidateFileID(id string) error {
	if !safeFileID.MatchString(id) {
		return fmt.Errorf("invalid file ID")
	}
	return nil
}

func catalogQuery(in v2.FileCatalogInput) (string, error) {
	if in.Limit < 0 || in.Limit > 100 || in.ThroughSequence != nil && *in.ThroughSequence < 0 {
		return "", fmt.Errorf("invalid catalog limit or sequence")
	}
	q := url.Values{}
	if in.Limit > 0 {
		q.Set("limit", strconv.Itoa(in.Limit))
	}
	if in.Cursor != "" {
		q.Set("cursor", in.Cursor)
	}
	if in.ThroughSequence != nil {
		q.Set("through_sequence", strconv.FormatInt(*in.ThroughSequence, 10))
	}
	if len(q) == 0 {
		return "", nil
	}
	return "?" + q.Encode(), nil
}

func ValidateFileCatalogEntry(project string, entry v2.FileCatalogEntry, through *int64) error {
	if err := ValidateFileID(entry.ID); err != nil {
		return err
	}
	if entry.ProjectID != project || entry.SizeBytes < 0 || entry.SizeBytes > v2.MaxFileBytes || len(entry.SHA256) != 64 || strings.Trim(entry.SHA256, "0123456789abcdef") != "" {
		return fmt.Errorf("invalid catalog file identity, size, or checksum")
	}
	if entry.ReferenceCount < 1 || len(entry.References) > 5 || entry.ReferenceCount < len(entry.References) || entry.ReferencesComplete != (entry.ReferenceCount == len(entry.References)) {
		return fmt.Errorf("invalid file reference preview")
	}
	return validateFileReferences(entry.References, through)
}
func validateFileReferences(refs []v2.FileReference, through *int64) error {
	seen := map[string]bool{}
	for _, ref := range refs {
		key := ref.EventID + "/" + ref.Relation
		if ref.EventID == "" || ref.Sequence < 1 || through != nil && ref.Sequence > *through || seen[key] {
			return fmt.Errorf("invalid or duplicate file reference")
		}
		if ref.Relation != "attachment" && ref.Relation != "derived_from" {
			return fmt.Errorf("invalid file reference relation")
		}
		if ref.Type != "note" && ref.Type != "log" && ref.Type != "derived" {
			return fmt.Errorf("invalid file reference type")
		}
		seen[key] = true
	}
	return nil
}
func (c *Client) ListFiles(ctx context.Context, project string, in v2.FileCatalogInput) (out v2.FileCatalogPage, err error) {
	q, err := catalogQuery(in)
	if err != nil {
		return out, err
	}
	err = c.doJSON(ctx, http.MethodGet, projectPath(project, "/files/catalog")+q, nil, &out)
	if err != nil {
		return out, err
	}
	if out.ProjectID != project || out.ThroughSequence < 0 || in.ThroughSequence != nil && out.ThroughSequence != *in.ThroughSequence || len(out.Files) > 100 {
		return out, fmt.Errorf("invalid catalog snapshot")
	}
	seen := map[string]bool{}
	for _, entry := range out.Files {
		if seen[entry.ID] {
			return out, fmt.Errorf("duplicate file in catalog page")
		}
		seen[entry.ID] = true
		if err = ValidateFileCatalogEntry(project, entry, &out.ThroughSequence); err != nil {
			return out, err
		}
	}
	return out, nil
}
func (c *Client) FileMetadata(ctx context.Context, project, fileID string, through *int64) (out v2.FileCatalogEntry, err error) {
	if err = ValidateFileID(fileID); err != nil {
		return out, err
	}
	q, err := catalogQuery(v2.FileCatalogInput{ThroughSequence: through})
	if err != nil {
		return out, err
	}
	err = c.doJSON(ctx, http.MethodGet, projectPath(project, "/files/"+url.PathEscape(fileID)+"/metadata")+q, nil, &out)
	if err == nil {
		if out.ID != fileID {
			return out, fmt.Errorf("server returned unexpected file metadata")
		}
		err = ValidateFileCatalogEntry(project, out, through)
	}
	return
}
func (c *Client) FileReferences(ctx context.Context, project, fileID string, in v2.FileCatalogInput) (out v2.FileReferencesPage, err error) {
	if err = ValidateFileID(fileID); err != nil {
		return out, err
	}
	q, err := catalogQuery(in)
	if err != nil {
		return out, err
	}
	err = c.doJSON(ctx, http.MethodGet, projectPath(project, "/files/"+url.PathEscape(fileID)+"/references")+q, nil, &out)
	if err != nil {
		return out, err
	}
	if out.ProjectID != project || out.FileID != fileID || out.ThroughSequence < 0 || in.ThroughSequence != nil && out.ThroughSequence != *in.ThroughSequence || len(out.References) > 100 {
		return out, fmt.Errorf("invalid file references snapshot")
	}
	err = validateFileReferences(out.References, &out.ThroughSequence)
	return
}
