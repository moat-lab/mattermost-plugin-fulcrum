package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	rexec "github.com/Mouriya-Emma/rexec-go"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// rexecRunTimeout is the per-call ceiling for fulcrum CLI invocations
// triggered by slash or button. CLI verbs in the contract are read-mostly and
// finish well under a few seconds; a 30s ceiling matches the rexecd default.
const rexecRunTimeout = 30 * time.Second

// buildAutocompleteTree returns the root AutocompleteData for `/f`. The verb
// surface mirrors the CLI verb catalogue in fulcrum cli/JSON_SCHEMA.md so a
// user pressing Tab inside Mattermost sees the same shape the plugin will
// invoke over rexec-go.
func buildAutocompleteTree() *model.AutocompleteData {
	root := model.NewAutocompleteData(slashTrigger, "[verb]", "Fulcrum verbs.")

	root.AddCommand(model.NewAutocompleteData("dashboard", "", "Aggregate task/app summary."))

	tasks := model.NewAutocompleteData("tasks", "<sub>", "Task operations.")
	tasksList := model.NewAutocompleteData("list", "", "List tasks with filters.")
	tasksList.AddNamedStaticListArgument("status", "Task status filter", false, []model.AutocompleteListItem{
		{Item: "active", Hint: "default — everything except done/canceled"},
		{Item: "todo", Hint: "TO_DO"},
		{Item: "doing", Hint: "IN_PROGRESS"},
		{Item: "review", Hint: "IN_REVIEW"},
		{Item: "done", Hint: "DONE"},
		{Item: "canceled", Hint: "CANCELED"},
	})
	tasksList.AddNamedStaticListArgument("priority", "Priority filter", false, []model.AutocompleteListItem{
		{Item: "high"}, {Item: "medium"}, {Item: "low"},
	})
	tasksList.AddNamedTextArgument("project", "Project slug or id", "<project>", "", false)
	tasksList.AddNamedTextArgument("tag", "Tag name", "<tag>", "", false)
	tasksList.AddNamedTextArgument("page", "Page number (1-based)", "<n>", "^[0-9]+$", false)
	tasks.AddCommand(tasksList)

	tasksGet := model.NewAutocompleteData("get", "<id>", "Show one task.")
	tasksGet.AddTextArgument("Task id", "<id>", "")
	tasks.AddCommand(tasksGet)

	tasksCreate := model.NewAutocompleteData("create", "--title=<t>", "Create a task.")
	tasksCreate.AddNamedTextArgument("title", "Task title", "<title>", "", true)
	tasksCreate.AddNamedTextArgument("project", "Project slug or id", "<project>", "", false)
	tasksCreate.AddNamedStaticListArgument("priority", "Priority", false, []model.AutocompleteListItem{
		{Item: "high"}, {Item: "medium"}, {Item: "low"},
	})
	tasks.AddCommand(tasksCreate)

	tasksSetStatus := model.NewAutocompleteData("set-status", "<id> <status>", "Set task status.")
	tasksSetStatus.AddTextArgument("Task id", "<id>", "")
	tasksSetStatus.AddStaticListArgument("Status", true, []model.AutocompleteListItem{
		{Item: "todo"}, {Item: "doing"}, {Item: "review"}, {Item: "done"}, {Item: "canceled"},
	})
	tasks.AddCommand(tasksSetStatus)

	tasksSetPriority := model.NewAutocompleteData("set-priority", "<id> <priority>", "Set task priority.")
	tasksSetPriority.AddTextArgument("Task id", "<id>", "")
	tasksSetPriority.AddStaticListArgument("Priority", true, []model.AutocompleteListItem{
		{Item: "high"}, {Item: "medium"}, {Item: "low"},
	})
	tasks.AddCommand(tasksSetPriority)

	tasks.AddCommand(simpleVerb("diff", "<id>", "Show task diff."))
	tasks.AddCommand(simpleVerb("start-agent", "<id>", "Start the task's coding agent."))
	tasks.AddCommand(simpleVerb("kill-agent", "<id>", "Stop the task's coding agent."))
	root.AddCommand(tasks)

	apps := model.NewAutocompleteData("apps", "<sub>", "App operations.")
	apps.AddCommand(model.NewAutocompleteData("list", "", "List apps."))
	apps.AddCommand(simpleVerb("get", "<id>", "Show one app."))
	apps.AddCommand(simpleVerb("deploy", "<id>", "Deploy an app."))
	apps.AddCommand(simpleVerb("stop", "<id>", "Stop an app."))
	rollback := model.NewAutocompleteData("rollback", "<id> <deployment-id>", "Roll back a deployment.")
	rollback.AddTextArgument("App id", "<id>", "")
	rollback.AddTextArgument("Deployment id", "<deployment-id>", "")
	apps.AddCommand(rollback)
	logs := model.NewAutocompleteData("logs", "<id>", "Tail app logs.")
	logs.AddTextArgument("App id", "<id>", "")
	logs.AddNamedTextArgument("service", "Service name", "<service>", "", false)
	logs.AddNamedTextArgument("tail", "Tail line count", "<n>", "^[0-9]+$", false)
	apps.AddCommand(logs)
	root.AddCommand(apps)

	search := model.NewAutocompleteData("search", "<query>", "Cross-entity search.")
	search.AddTextArgument("Query", "<query>", "")
	search.AddNamedTextArgument("limit", "Max results", "<n>", "^[0-9]+$", false)
	root.AddCommand(search)

	root.AddCommand(model.NewAutocompleteData("monitor", "", "Host resource snapshot."))

	jobs := model.NewAutocompleteData("jobs", "", "List background jobs.")
	jobs.AddNamedStaticListArgument("scope", "Job scope", false, []model.AutocompleteListItem{
		{Item: "all"}, {Item: "user"}, {Item: "system"},
	})
	root.AddCommand(jobs)

	root.AddCommand(model.NewAutocompleteData("projects", "", "List projects."))

	return root
}

func simpleVerb(name, hint, help string) *model.AutocompleteData {
	v := model.NewAutocompleteData(name, hint, help)
	v.AddTextArgument("id", "<id>", "")
	return v
}

// ExecuteCommand handles `/f ...`. It forwards argv to rexecd, parses the
// fulcrum CLI JSON envelope, and posts the rendered attachment as a real bot
// post (UserId = botUserID) so downstream UpdatePost calls from button
// handlers are accepted by the server.
func (p *Plugin) ExecuteCommand(_ *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	client := p.getClient()
	rc := p.getRexec()
	botID := p.getBotUserID()
	if client == nil || rc == nil || botID == "" {
		return ephemeral("fulcrum plugin is not fully activated"), nil
	}

	argv, err := buildCLIArgv(args.Command)
	if err != nil {
		return ephemeral(err.Error()), nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), rexecRunTimeout)
	defer cancel()

	res, err := rc.Run(ctx, argv, rexec.WithTimeout(rexecRunTimeout))
	if err != nil {
		return ephemeral(fmt.Sprintf("fulcrum unreachable: %v", err)), nil
	}
	if res.ExitCode != 0 {
		stderr := strings.TrimSpace(string(res.Stderr))
		if stderr == "" {
			stderr = fmt.Sprintf("exit %d", res.ExitCode)
		}
		return ephemeral("fulcrum error: " + stderr), nil
	}

	att, renderErr := renderEnvelope(res.Stdout)
	if renderErr != nil {
		return ephemeral(fmt.Sprintf("render error: %v (raw: %s)", renderErr, truncate(string(res.Stdout), 200))), nil
	}

	post := &model.Post{
		ChannelId: args.ChannelId,
		UserId:    botID,
	}
	model.ParseSlackAttachment(post, []*model.SlackAttachment{att})
	if err := client.Post.CreatePost(post); err != nil {
		return ephemeral(fmt.Sprintf("create post failed: %v", err)), nil
	}

	return &model.CommandResponse{}, nil
}

// buildCLIArgv tokenizes the raw slash command string into the argv passed to
// `fulcrum`. The first token is always `/f` and is dropped; `--json` is
// appended so the CLI emits the contract envelope.
func buildCLIArgv(raw string) ([]string, error) {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) == 0 {
		return nil, errors.New("empty command")
	}
	// fields[0] is "/f"; drop it.
	verbArgs := fields[1:]
	if len(verbArgs) == 0 {
		// Empty `/f` defaults to the dashboard verb.
		verbArgs = []string{"dashboard"}
	}
	argv := append([]string{"fulcrum"}, verbArgs...)
	argv = append(argv, "--json")
	return argv, nil
}

func ephemeral(text string) *model.CommandResponse {
	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         text,
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

