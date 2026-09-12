package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"event-driven-context/internal/core"
	"event-driven-context/internal/mcpserver"
	"event-driven-context/internal/v2"
	"event-driven-context/internal/v2client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type remoteMCPBackend struct{ client *v2client.Client }

func remoteResult[T any](out T, err error) (T, error) {
	return out, remoteError(err)
}

// remoteError preserves the public HTTP error contract at the stdio MCP
// boundary. mcpserver intentionally hides unknown implementation errors, so a
// typed API error must be converted back to the shared V2 error type first.
func remoteError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *v2client.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	code := apiErr.Code
	if code == "" {
		switch apiErr.Status {
		case http.StatusUnauthorized:
			code = "unauthenticated"
		case http.StatusForbidden:
			code = "forbidden"
		case http.StatusNotFound:
			code = "not_found"
		case http.StatusConflict:
			code = "conflict"
		case http.StatusRequestEntityTooLarge:
			code = "too_large"
		case http.StatusTooManyRequests:
			code = "rate_limited"
		default:
			return err
		}
	}
	message := apiErr.Message
	if message == "" {
		message = http.StatusText(apiErr.Status)
	}
	return &v2.Error{Code: code, Message: message}
}

func (b remoteMCPBackend) ListProjects(ctx context.Context, _ core.Empty) (core.Projects, error) {
	return remoteResult(b.client.ListProjects(ctx))
}
func (b remoteMCPBackend) CreateProject(ctx context.Context, in core.ProjectInput) (core.Project, error) {
	return remoteResult(b.client.CreateProject(ctx, in))
}
func (b remoteMCPBackend) ListMembers(ctx context.Context, in mcpserver.V2ProjectInput) (core.Members, error) {
	return remoteResult(b.client.ListMembers(ctx, in.ProjectID))
}
func (b remoteMCPBackend) AddMember(ctx context.Context, in mcpserver.V2AddMemberInput) (core.User, error) {
	return remoteResult(b.client.AddMember(ctx, in.ProjectID, in.Username))
}
func (b remoteMCPBackend) RecordEvents(ctx context.Context, in mcpserver.V2RecordEventsInput) (v2.RecordEventsResult, error) {
	out, err := b.client.RecordEvents(ctx, in.ProjectID, v2.RecordEventsInput{Events: in.Events})
	var batchErr *v2client.BatchError
	if errors.As(err, &batchErr) {
		// Per-item conflict/invalid results are the MCP result, not a tool-level
		// failure. The CLI command still uses BatchError for its non-zero exit.
		return out, nil
	}
	return remoteResult(out, err)
}
func (b remoteMCPBackend) QueryEvents(ctx context.Context, in mcpserver.V2QueryEventsInput) (v2.EventsPage, error) {
	return remoteResult(b.client.QueryEvents(ctx, in.ProjectID, v2.QueryEventsInput{Types: in.Types, Metadata: in.Metadata, Source: in.Source, RefsTo: in.RefsTo, AfterSequence: in.AfterSequence, From: in.From, To: in.To, TimeField: in.TimeField, Limit: in.Limit, Cursor: in.Cursor}))
}
func (b remoteMCPBackend) GetEvent(ctx context.Context, in mcpserver.V2GetEventInput) (v2.Event, error) {
	return remoteResult(b.client.GetEvent(ctx, in.ProjectID, in.EventID))
}
func (b remoteMCPBackend) ListMetadata(ctx context.Context, in mcpserver.V2MetadataInput) (v2.MetadataResult, error) {
	return remoteResult(b.client.ListMetadata(ctx, in.ProjectID, in.Key))
}
func (b remoteMCPBackend) UploadFile(ctx context.Context, in mcpserver.V2UploadFileInput) (v2.FileInfo, error) {
	if len(in.DataBase64) > base64.StdEncoding.EncodedLen(mcpserver.MaxV2MCPFileBytes) {
		return v2.FileInfo{}, &v2.Error{Code: "too_large", Message: "MCP file exceeds 1 MiB"}
	}
	data, err := base64.StdEncoding.Strict().DecodeString(in.DataBase64)
	if err != nil {
		return v2.FileInfo{}, &v2.Error{Code: "invalid_input", Message: "data_base64 is invalid"}
	}
	if len(data) > mcpserver.MaxV2MCPFileBytes {
		return v2.FileInfo{}, &v2.Error{Code: "too_large", Message: "MCP file exceeds 1 MiB"}
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	if in.SHA256 != "" {
		decoded, decodeErr := hex.DecodeString(in.SHA256)
		if decodeErr != nil || len(decoded) != sha256.Size {
			return v2.FileInfo{}, &v2.Error{Code: "invalid_input", Message: "sha256 must be 64 hexadecimal characters"}
		}
		if !strings.EqualFold(in.SHA256, digest) {
			return v2.FileInfo{}, &v2.Error{Code: "invalid_input", Message: "sha256 does not match content"}
		}
	}
	return remoteResult(b.client.PutFile(ctx, in.ProjectID, v2.FileUpload{Filename: in.Filename, MediaType: in.MediaType, SHA256: digest, SizeBytes: int64(len(data)), Reader: bytes.NewReader(data)}))
}
func (b remoteMCPBackend) GetFile(ctx context.Context, in mcpserver.V2GetFileInput) (mcpserver.V2FileResult, error) {
	tmp, err := os.CreateTemp("", "edc-mcp-file-*")
	if err != nil {
		return mcpserver.V2FileResult{}, err
	}
	name := tmp.Name()
	defer os.Remove(name)
	info, err := b.client.GetFile(ctx, in.ProjectID, in.FileID, tmp)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return mcpserver.V2FileResult{}, remoteError(err)
	}
	out := mcpserver.V2FileResult{FileInfo: v2.FileInfo{ID: info.FileID, ProjectID: in.ProjectID, Filename: info.Filename, MediaType: info.MediaType, SizeBytes: info.SizeBytes, SHA256: info.SHA256}}
	if info.SizeBytes > mcpserver.MaxV2MCPFileBytes {
		out.ContentOmitted = true
		return out, nil
	}
	f, err := os.Open(name)
	if err != nil {
		return mcpserver.V2FileResult{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, mcpserver.MaxV2MCPFileBytes+1))
	if err != nil {
		return mcpserver.V2FileResult{}, err
	}
	out.DataBase64 = base64.StdEncoding.EncodeToString(data)
	return out, nil
}
func (b remoteMCPBackend) ListState(ctx context.Context, in mcpserver.V2ListStateInput) (v2.StatesResult, error) {
	return remoteResult(b.client.ListState(ctx, in.ProjectID, in.Prefix))
}
func (b remoteMCPBackend) GetState(ctx context.Context, in mcpserver.V2GetStateInput) (v2.StatesResult, error) {
	if len(in.Keys) < 1 || len(in.Keys) > 100 {
		return v2.StatesResult{}, &v2.Error{Code: "invalid_input", Message: "keys must contain 1 to 100 items"}
	}
	if in.Version != nil && *in.Version < 1 {
		return v2.StatesResult{}, &v2.Error{Code: "invalid_input", Message: "version must be positive"}
	}
	listed, err := remoteResult(b.client.ListState(ctx, in.ProjectID, ""))
	if err != nil {
		return v2.StatesResult{}, err
	}
	latestByKey := make(map[string]v2.State, len(listed.States))
	for _, state := range listed.States {
		latestByKey[state.Key] = state
	}
	out := v2.StatesResult{States: make([]v2.State, 0, len(in.Keys)), LatestSequence: listed.LatestSequence}
	for _, key := range in.Keys {
		if in.Version == nil {
			if state, ok := latestByKey[key]; ok {
				out.States = append(out.States, state)
				continue
			}
		}
		state, err := b.client.GetState(ctx, in.ProjectID, key, in.Version)
		if err != nil {
			var apiErr *v2client.APIError
			if errors.As(err, &apiErr) && (apiErr.Code == "not_found" || apiErr.Status == http.StatusNotFound) {
				continue
			}
			return v2.StatesResult{}, remoteError(err)
		}
		out.States = append(out.States, state)
		if latest := state.BasedOnSequence + state.Lag; latest > out.LatestSequence {
			out.LatestSequence = latest
		}
	}
	for i := range out.States {
		out.States[i].Lag = out.LatestSequence - out.States[i].BasedOnSequence
	}
	return out, nil
}
func (b remoteMCPBackend) PutState(ctx context.Context, in mcpserver.V2PutStateInput) (v2.State, error) {
	return remoteResult(b.client.PutState(ctx, in.ProjectID, v2.PutStateInput{Key: in.Key, ExpectedVersion: in.ExpectedVersion, Content: in.Content, Data: in.Data, BasedOnSequence: in.BasedOnSequence, Refs: in.Refs, AsPluginID: in.AsPluginID}))
}

func runMCP(a *app, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("mcp takes no arguments")
	}
	return mcpserver.NewV2(remoteMCPBackend{client: a.client}).Run(a.ctx, &mcp.StdioTransport{})
}

var _ mcpserver.V2Backend = remoteMCPBackend{}
