package core

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"mime"
	"sort"
	"strings"
	"unicode/utf8"
)

// Canonical JSON ignores object key order and whitespace, preserves numbers exactly.
func canonical(raw json.RawMessage) (string, string, error) {
	if !json.Valid(raw) {
		return "", "", Invalid("metadata must contain valid JSON values")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return "", "", Invalid("invalid metadata")
	}
	typ := "null"
	switch v.(type) {
	case string:
		typ = "string"
	case json.Number:
		typ = "number"
	case bool:
		typ = "boolean"
	case []any:
		typ = "array"
	case map[string]any:
		typ = "object"
	}
	b, err := json.Marshal(v)
	return typ, string(b), err
}
func normalizeMetadata(m map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	if len(m) > 128 {
		return nil, Invalid("metadata max 128 top-level fields")
	}
	out := map[string]json.RawMessage{}
	for k, v := range m {
		if len(k) == 0 || len(k) > 128 || !utf8.ValidString(k) {
			return nil, Invalid("metadata keys must be 1-128 UTF-8 bytes")
		}
		_, c, err := canonical(v)
		if err != nil {
			return nil, err
		}
		out[k] = json.RawMessage(c)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, Invalid("invalid metadata")
	}
	if len(b) > MaxMetadataBytes {
		return nil, Invalid("metadata max 32 KiB")
	}
	return out, nil
}
func prepareRecord(in RecordInput) (RecordInput, []byte, error) {
	var err error
	in.Metadata, err = normalizeMetadata(in.Metadata)
	if err != nil {
		return in, nil, err
	}
	if len(in.IdempotencyKey) > 128 {
		return in, nil, Invalid("idempotency_key max 128 bytes")
	}
	if in.Action != nil {
		action := *in.Action
		action.SourceEventIDs, err = normalizeEventIDs(action.SourceEventIDs)
		if err != nil {
			return in, nil, err
		}
		action.SupersedesEventIDs, err = normalizeEventIDs(action.SupersedesEventIDs)
		if err != nil {
			return in, nil, err
		}
		switch action.Kind {
		case "confirmation":
			if len(action.SourceEventIDs) == 0 {
				return in, nil, Invalid("confirmation requires source_event_ids")
			}
		case "correction":
			if len(action.SupersedesEventIDs) == 0 {
				return in, nil, Invalid("correction requires supersedes_event_ids")
			}
		default:
			return in, nil, Invalid("action.kind must be confirmation or correction")
		}
		if len(action.SourceEventIDs)+len(action.SupersedesEventIDs) > 128 {
			return in, nil, Invalid("action max 128 event references")
		}
		in.Action = &action
	}
	if in.OccurredAt != "" {
		in.OccurredAt, err = normalizedTime(in.OccurredAt)
		if err != nil {
			return in, nil, err
		}
	}
	var data []byte
	switch in.Content.Kind {
	case "text":
		if in.Content.Text == nil || in.Content.File != nil {
			return in, nil, Invalid("text content requires text and no file")
		}
		if len(*in.Content.Text) > MaxContentBytes || !utf8.ValidString(*in.Content.Text) {
			return in, nil, Invalid("text must be UTF-8, max 1 MiB")
		}
	case "file":
		if in.Content.File == nil || in.Content.Text != nil {
			return in, nil, Invalid("file content requires file and no text")
		}
		f := *in.Content.File
		in.Content.File = &f
		if err = validateFilename(f.Filename); err != nil {
			return in, nil, err
		}
		media, params, e := mime.ParseMediaType(f.MediaType)
		if e != nil || !strings.HasPrefix(media, "text/") {
			return in, nil, Invalid("declare a supported media_type: text/*; binary files are not yet accepted")
		}
		for k, v := range params {
			if k != "charset" || !strings.EqualFold(v, "utf-8") {
				return in, nil, Invalid("only UTF-8 text files are accepted")
			}
		}
		if len(f.DataBase64) > base64.StdEncoding.EncodedLen(MaxContentBytes)+2 {
			return in, nil, Invalid("file max 1 MiB")
		}
		data, e = base64.StdEncoding.Strict().DecodeString(f.DataBase64)
		if e != nil || len(data) > MaxContentBytes || !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
			return in, nil, Invalid("file must contain valid base64-encoded UTF-8 text, no NUL bytes, max 1 MiB")
		}
		f.MediaType = media
		f.DataBase64 = base64.StdEncoding.EncodeToString(data)
	default:
		return in, nil, Invalid("content.kind must be text or file")
	}
	if in.Action != nil && in.Content.Kind != "text" {
		return in, nil, Invalid("event actions require text content")
	}
	return in, data, nil
}

func normalizeEventIDs(ids []string) ([]string, error) {
	out := append([]string(nil), ids...)
	for _, id := range out {
		if id == "" || len(id) > 128 || !utf8.ValidString(id) || strings.ContainsAny(id, "/\\\x00\r\n") {
			return nil, Invalid("event references must be 1-128 safe UTF-8 bytes")
		}
	}
	sort.Strings(out)
	deduped := out[:0]
	for _, id := range out {
		if len(deduped) == 0 || deduped[len(deduped)-1] != id {
			deduped = append(deduped, id)
		}
	}
	return deduped, nil
}

func prepareMediaRecord(in MediaRecordInput) (RecordInput, []byte, error) {
	metadata, err := normalizeMetadata(in.Metadata)
	if err != nil {
		return RecordInput{}, nil, err
	}
	if len(in.IdempotencyKey) > 128 {
		return RecordInput{}, nil, Invalid("idempotency_key max 128 bytes")
	}
	if in.OccurredAt != "" {
		in.OccurredAt, err = normalizedTime(in.OccurredAt)
		if err != nil {
			return RecordInput{}, nil, err
		}
	}
	if err = validateFilename(in.Filename); err != nil {
		return RecordInput{}, nil, err
	}
	if len(in.Data) == 0 {
		return RecordInput{}, nil, Invalid("media file must not be empty")
	}
	if len(in.Data) > MaxMediaBytes {
		return RecordInput{}, nil, TooLarge("media file max 20 MiB")
	}
	mediaType, err := validateMedia(in.MediaType, in.Data)
	if err != nil {
		return RecordInput{}, nil, err
	}
	record := RecordInput{
		ProjectID:      in.ProjectID,
		Content:        ContentInput{Kind: "file", File: &FileInput{Filename: in.Filename, MediaType: mediaType}},
		Metadata:       metadata,
		OccurredAt:     in.OccurredAt,
		IdempotencyKey: in.IdempotencyKey,
	}
	return record, in.Data, nil
}

func validateFilename(filename string) error {
	if filename == "" || len(filename) > 255 || !utf8.ValidString(filename) || strings.ContainsAny(filename, "/\\\x00\r\n") || filename == "." || filename == ".." {
		return Invalid("filename must be a UTF-8 basename of 1-255 bytes")
	}
	return nil
}

func validateMedia(declared string, data []byte) (string, error) {
	mediaType, params, err := mime.ParseMediaType(declared)
	if err != nil || len(params) != 0 {
		return "", Invalid("declare a supported media_type: audio/mp4, audio/mpeg, audio/wav, or audio/ogg")
	}
	switch mediaType {
	case "audio/mp4":
		if !isAACMP4(data) {
			return "", Invalid("file content does not match audio/mp4 AAC")
		}
	case "audio/mpeg":
		if !isMP3(data) {
			return "", Invalid("file content does not match audio/mpeg")
		}
	case "audio/wav", "audio/x-wav", "audio/wave", "audio/vnd.wave":
		if !isWAV(data) {
			return "", Invalid("file content does not match audio/wav")
		}
		mediaType = "audio/wav"
	case "audio/ogg":
		if !ValidateAudioContent("audio/ogg", data) {
			return "", Invalid("file content does not match audio/ogg")
		}
	default:
		return "", Invalid("declare a supported media_type: audio/mp4, audio/mpeg, audio/wav, or audio/ogg")
	}
	return mediaType, nil
}
