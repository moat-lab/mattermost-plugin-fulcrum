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
	// host is required by fulcrum CLI in remote-only deployments
	// (fulcrum/server/routes/tasks.ts rejects body without hostId). Marked
	// optional in autocomplete because System Console may set DefaultHostID
	// to cover the common case; the plugin injects --host <DefaultHostID>
	// when the user omits this arg (see injectDefaultHostIfNeeded).
	tasksCreate.AddNamedTextArgument("host", "Host id (required in remote-only; defaults to System Console DefaultHostID if set)", "<host-id>", "", false)
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
	argv = injectDefaultHostIfNeeded(argv, p.getConfiguration().trimmedDefaultHostID())

	ctx, cancel := context.WithTimeout(context.Background(), rexecRunTimeout)
	defer cancel()

	res, err := rc.Run(ctx, argv, rexec.WithTimeout(rexecRunTimeout))
	if err != nil {
		return ephemeral(fmt.Sprintf("fulcrum unreachable: %v", err)), nil
	}

	// tasks.create surfaces every envelope-error code ephemerally per spike
	// §B.4.5 — the channel can't act on a task that didn't get created, so a
	// bot post would be noise. The CLI's MISSING_TITLE path exits with
	// ExitCodes.INVALID_ARGS (= 2) and writes the envelope to stdout, so this
	// check has to run before the bare exit-code ephemeral below; the
	// CREATE_FAILED path may exit 0 (RUNTIME_ERROR is undefined in the CLI's
	// ExitCodes enum, falling back to 0), so the same check covers it.
	if verb, errCode, errMsg, parseErr := parseEnvelopeOutcome(res.Stdout); parseErr == nil && verb == "tasks.create" && errCode != "" {
		return ephemeral(taskQuickCreateBusinessErrorMessage(errCode, errMsg)), nil
	}

	if res.ExitCode != 0 {
		stderr := strings.TrimSpace(string(res.Stderr))
		if stderr == "" {
			stderr = fmt.Sprintf("exit %d", res.ExitCode)
		}
		return ephemeral("fulcrum error: " + stderr), nil
	}

	// apps.logs surfaces a subset of business-error codes ephemerally per
	// spike §B.8.5 (app_not_found, service_not_found). Other codes — notably
	// logs_unavailable — fall through to the renderer so the user sees a
	// colorError card with Refresh + Back to app instead of a one-line
	// ephemeral. Check before rendering so we don't create a bot post that
	// only the slashing user can see contextualized.
	if verb, errCode, errMsg, parseErr := parseEnvelopeOutcome(res.Stdout); parseErr == nil && verb == "apps.logs" && appLogsEphemeralCodes[errCode] {
		hints, argvAppID := extractAppLogsHints(argv)
		return ephemeral(appLogsBusinessErrorMessage(errCode, errMsg, argvAppID, hints.RequestedService)), nil
	}

	// tasks.diff surfaces task_not_found ephemerally per spike §B.5.5: the
	// channel can't usefully act on a task that doesn't exist, so the
	// colorError bot card is reserved for git_unavailable (and any future
	// non-ephemeral diff code). The renderer fall-through handles
	// git_unavailable by routing it to renderTaskDiffBusinessError.
	if verb, errCode, errMsg, parseErr := parseEnvelopeOutcome(res.Stdout); parseErr == nil && verb == "tasks.diff" && taskDiffEphemeralCodes[errCode] {
		return ephemeral(taskDiffBusinessErrorMessage(errCode, errMsg)), nil
	}

	// search surfaces query_too_short / invalid_limit ephemerally per spike
	// §B.9.5: the channel can't act on a malformed query, so the colorError
	// bot card is reserved for envelope-level / unknown codes (FETCH_FAILED
	// etc.) that fall through to renderBusinessError.
	if verb, errCode, errMsg, parseErr := parseEnvelopeOutcome(res.Stdout); parseErr == nil && verb == "search" && searchEphemeralCodes[errCode] {
		return ephemeral(searchBusinessErrorMessage(errCode, errMsg)), nil
	}

	// jobs surfaces unknown_scope ephemerally per spike §B.11.5: a malformed
	// --scope is a slash-input error the channel can't act on, so the
	// colorError bot card is reserved for systemd_unavailable (and any
	// future non-ephemeral code) which renderJobsBusinessError handles via
	// fall-through to the renderer.
	if verb, errCode, errMsg, parseErr := parseEnvelopeOutcome(res.Stdout); parseErr == nil && verb == "jobs" && jobsEphemeralCodes[errCode] {
		return ephemeral(jobsBusinessErrorMessage(errCode, errMsg)), nil
	}

	att, renderErr := renderEnvelopeAtForRequest(res.Stdout, time.Now(), args.UserId, argv)
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

// injectDefaultHostIfNeeded inserts `--host <defaultHostID>` into a
// `tasks create` argv when the user did not pass `--host` themselves. fulcrum
// CLI rejects the underlying POST when remote-only mode + missing hostId
// (fulcrum/server/routes/tasks.ts), so without this injection an admin-set
// DefaultHostID would still result in a CREATE_FAILED ephemeral.
//
// The function is a no-op when:
//   - defaultHostID is empty (admin hasn't configured a default)
//   - argv does not target the `tasks create` verb
//   - argv already carries an explicit `--host` token (per-call override)
//
// The injection is placed before the trailing `--json` flag to keep the
// argv shape predictable for parseEnvelopeOutcome / renderer dispatchers.
func injectDefaultHostIfNeeded(argv []string, defaultHostID string) []string {
	if defaultHostID == "" {
		return argv
	}
	if !isTasksCreateArgv(argv) {
		return argv
	}
	if hasHostArg(argv) {
		return argv
	}
	n := len(argv)
	if n > 0 && argv[n-1] == "--json" {
		out := make([]string, 0, n+2)
		out = append(out, argv[:n-1]...)
		out = append(out, "--host", defaultHostID, "--json")
		return out
	}
	out := make([]string, 0, n+2)
	out = append(out, argv...)
	out = append(out, "--host", defaultHostID)
	return out
}

// isTasksCreateArgv returns true when argv targets the `tasks create` verb.
// The plugin always prepends "fulcrum" before forwarding to rexecd, so the
// positional shape is fixed.
func isTasksCreateArgv(argv []string) bool {
	return len(argv) >= 3 && argv[0] == "fulcrum" && argv[1] == "tasks" && argv[2] == "create"
}

// hasHostArg returns true when argv contains an explicit `--host` token in
// either space-separated form (`--host vctcn-app1`) or equals-bound form
// (`--host=vctcn-app1`). Both are valid fulcrum CLI input shapes.
func hasHostArg(argv []string) bool {
	for _, tok := range argv {
		if tok == "--host" || strings.HasPrefix(tok, "--host=") {
			return true
		}
	}
	return false
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

