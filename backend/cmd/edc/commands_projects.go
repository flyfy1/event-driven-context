package main

import (
	"fmt"

	"event-driven-context/internal/core"
)

func (a *app) project(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("project requires create, list, members or add-member")
	}
	command := args[0]
	f := a.flags("project " + command)
	switch command {
	case "list":
		if err := parse(f, args[1:]); err != nil {
			return err
		}
		out, err := a.client.ListProjects(a.ctx)
		return a.result(out, err)
	case "create":
		name := f.String("name", "", "project name")
		description := f.String("description", "", "description")
		if err := parse(f, args[1:]); err != nil {
			return err
		}
		if *name == "" {
			return fmt.Errorf("--name is required")
		}
		out, err := a.client.CreateProject(a.ctx, core.ProjectInput{Name: *name, Description: *description})
		return a.result(out, err)
	case "members":
		projectID := f.String("project", "", "project ID")
		if err := parse(f, args[1:]); err != nil {
			return err
		}
		if err := a.requiredProject(projectID); err != nil {
			return err
		}
		out, err := a.client.ListMembers(a.ctx, *projectID)
		return a.result(out, err)
	case "add-member":
		projectID := f.String("project", "", "project ID")
		username := f.String("username", "", "registered username")
		email := f.String("email", "", "registered email")
		if err := parse(f, args[1:]); err != nil {
			return err
		}
		if err := a.requiredProject(projectID); err != nil {
			return err
		}
		if (*username == "") == (*email == "") {
			return fmt.Errorf("exactly one of --username or --email is required")
		}
		if *email != "" {
			out, err := a.client.AddMemberByEmail(a.ctx, *projectID, *email)
			return a.result(out, err)
		}
		out, err := a.client.AddMember(a.ctx, *projectID, *username)
		return a.result(out, err)
	default:
		return fmt.Errorf("unknown project command %q", command)
	}
}
