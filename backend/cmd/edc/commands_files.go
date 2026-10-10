package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"event-driven-context/internal/localcollection"
	"event-driven-context/internal/v2"
)

func openInputFile(stdin io.Reader, path string) (*os.File, func(), string, int64, string, error) {
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return nil, nil, "", 0, "", err
		}
		st, err := f.Stat()
		if err != nil {
			f.Close()
			return nil, nil, "", 0, "", err
		}
		if !st.Mode().IsRegular() || st.Size() > v2.MaxFileBytes {
			f.Close()
			return nil, nil, "", 0, "", fmt.Errorf("file must be regular and at most %d bytes", v2.MaxFileBytes)
		}
		h := sha256.New()
		if _, err = io.Copy(h, f); err != nil {
			f.Close()
			return nil, nil, "", 0, "", err
		}
		if _, err = f.Seek(0, io.SeekStart); err != nil {
			f.Close()
			return nil, nil, "", 0, "", err
		}
		return f, func() { _ = f.Close() }, filepath.Base(path), st.Size(), hex.EncodeToString(h.Sum(nil)), nil
	}
	tmp, err := os.CreateTemp("", "edc-push-*")
	if err != nil {
		return nil, nil, "", 0, "", err
	}
	cleanup := func() { name := tmp.Name(); _ = tmp.Close(); _ = os.Remove(name) }
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(stdin, v2.MaxFileBytes+1))
	if err != nil || n > v2.MaxFileBytes {
		cleanup()
		if err != nil {
			return nil, nil, "", 0, "", err
		}
		return nil, nil, "", 0, "", fmt.Errorf("file exceeds %d bytes", v2.MaxFileBytes)
	}
	if _, err = tmp.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, nil, "", 0, "", err
	}
	return tmp, cleanup, "stdin", n, hex.EncodeToString(h.Sum(nil)), nil
}

func (a *app) file(args []string) error {
	if len(args) == 0 || args[0] != "get" {
		return fmt.Errorf("file requires get")
	}
	f := a.flags("file get")
	projectID := f.String("project", "", "project ID")
	output := f.String("o", "-", "output path or -")
	cache := f.String("cache", "", "collection directory for verified on-demand caching")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if err := a.requiredProject(projectID); err != nil {
		return err
	}
	if f.NArg() != 1 {
		return fmt.Errorf("file get requires one FILE_ID")
	}
	if *cache != "" {
		hasOutput := false
		f.Visit(func(value *flag.Flag) {
			if value.Name == "o" {
				hasOutput = true
			}
		})
		if hasOutput {
			return fmt.Errorf("--cache and -o are mutually exclusive")
		}
		collection, err := localcollection.Open(*cache, a.client.BaseURL, *projectID)
		if err != nil {
			return err
		}
		defer collection.Close()
		result, err := collection.GetFile(a.ctx, a.client, f.Arg(0))
		return a.result(result, err)
	}
	if *output == "-" {
		tmp, err := os.CreateTemp("", "edc-download-*")
		if err != nil {
			return err
		}
		name := tmp.Name()
		defer os.Remove(name)
		_, err = a.client.GetFile(a.ctx, *projectID, f.Arg(0), tmp)
		if closeErr := tmp.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
		verified, err := os.Open(name)
		if err != nil {
			return err
		}
		defer verified.Close()
		_, err = io.Copy(a.io.out, verified)
		return err
	}
	dir := filepath.Dir(*output)
	publish, err := os.CreateTemp(dir, ".edc-download-*")
	if err != nil {
		return err
	}
	publishName := publish.Name()
	defer os.Remove(publishName)
	info, err := a.client.GetFile(a.ctx, *projectID, f.Arg(0), publish)
	if err == nil {
		err = publish.Chmod(0600)
	}
	if err == nil {
		err = publish.Sync()
	}
	if closeErr := publish.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	// A hard link publishes the verified inode atomically and cannot replace an
	// existing path. The temporary file is in the destination directory, so the
	// operation stays on one filesystem.
	if err = os.Link(publishName, *output); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("output already exists: %s", *output)
		}
		return fmt.Errorf("publish download: %w", err)
	}
	return a.json(info)
}
