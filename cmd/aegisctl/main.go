package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"aegisrt/internal/controlclient"
)

const defaultServer = "http://127.0.0.1:18080"

type eventPage struct {
	Items        []json.RawMessage `json:"items"`
	Count        int               `json:"count"`
	NextSequence uint64            `json:"next_sequence"`
}

func main() {
	server := flag.String(
		"server",
		environmentOrDefault(
			"CAPSULERT_SERVER",
			environmentOrDefault(
				"AEGISRT_SERVER",
				defaultServer,
			),
		),
		"CAPSuleRT Runtime API URL",
	)

	timeout := flag.Duration(
		"timeout",
		10*time.Second,
		"HTTP request timeout",
	)

	flag.Usage = usage
	flag.Parse()

	arguments := flag.Args()

	if len(arguments) == 0 {
		usage()
		os.Exit(2)
	}

	client, err := controlclient.New(
		*server,
		*timeout,
	)
	if err != nil {
		fatal(err)
	}

	command := arguments[0]
	commandArgs := arguments[1:]

	ctx := context.Background()

	switch command {
	case "health":
		runJSON(ctx, client, "/healthz", nil)

	case "ready":
		runJSON(ctx, client, "/readyz", nil)

	case "status":
		runJSON(
			ctx,
			client,
			"/v1/runtime/status",
			nil,
		)

	case "agents":
		runAgents(ctx, client, commandArgs)

	case "agent":
		runAgent(ctx, client, commandArgs)

	case "events":
		runEvents(ctx, client, commandArgs)

	case "watch":
		runWatch(client, commandArgs)

	case "metrics":
		runText(ctx, client, "/metrics", nil)

	case "help", "-h", "--help":
		usage()

	default:
		fmt.Fprintf(
			os.Stderr,
			"unknown command %q\n\n",
			command,
		)

		usage()
		os.Exit(2)
	}
}

func runAgents(
	ctx context.Context,
	client *controlclient.Client,
	arguments []string,
) {
	flags := flag.NewFlagSet(
		"agents",
		flag.ExitOnError,
	)

	phase := flags.String(
		"phase",
		"",
		"filter by QUEUED, RUNNING, SUCCEEDED, FAILED or BLOCKED",
	)

	limit := flags.Int(
		"limit",
		200,
		"maximum number of records",
	)

	_ = flags.Parse(arguments)

	query := url.Values{}
	query.Set("limit", strconv.Itoa(*limit))

	if strings.TrimSpace(*phase) != "" {
		query.Set(
			"phase",
			strings.ToUpper(*phase),
		)
	}

	runJSON(
		ctx,
		client,
		"/v1/agents",
		query,
	)
}

func runAgent(
	ctx context.Context,
	client *controlclient.Client,
	arguments []string,
) {
	if len(arguments) != 1 {
		fatal(
			errors.New(
				"usage: capsulectl agent <agent-id>",
			),
		)
	}

	path := "/v1/agents/" +
		url.PathEscape(arguments[0])

	runJSON(ctx, client, path, nil)
}

func runEvents(
	ctx context.Context,
	client *controlclient.Client,
	arguments []string,
) {
	flags := flag.NewFlagSet(
		"events",
		flag.ExitOnError,
	)

	since := flags.Uint64(
		"since",
		0,
		"return events after this sequence",
	)

	limit := flags.Int(
		"limit",
		200,
		"maximum number of events",
	)

	kind := flags.String(
		"kind",
		"",
		"filter by event kind",
	)

	agentID := flags.String(
		"agent-id",
		"",
		"filter by Agent ID",
	)

	phase := flags.String(
		"phase",
		"",
		"filter by Agent phase",
	)

	_ = flags.Parse(arguments)

	query := eventQuery(
		*since,
		*limit,
		*kind,
		*agentID,
		*phase,
	)

	runJSON(
		ctx,
		client,
		"/v1/events",
		query,
	)
}

func runWatch(
	client *controlclient.Client,
	arguments []string,
) {
	flags := flag.NewFlagSet(
		"watch",
		flag.ExitOnError,
	)

	since := flags.Uint64(
		"since",
		0,
		"begin after this sequence",
	)

	limit := flags.Int(
		"limit",
		200,
		"events fetched per request",
	)

	interval := flags.Duration(
		"interval",
		time.Second,
		"polling interval",
	)

	kind := flags.String(
		"kind",
		"",
		"filter by event kind",
	)

	agentID := flags.String(
		"agent-id",
		"",
		"filter by Agent ID",
	)

	phase := flags.String(
		"phase",
		"",
		"filter by Agent phase",
	)

	jsonOutput := flags.Bool(
		"json",
		false,
		"print complete JSON events",
	)

	_ = flags.Parse(arguments)

	if *interval < 100*time.Millisecond {
		fatal(
			errors.New(
				"watch interval must be at least 100ms",
			),
		)
	}

	ctx, cancel := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer cancel()

	currentSequence := *since

	for {
		query := eventQuery(
			currentSequence,
			*limit,
			*kind,
			*agentID,
			*phase,
		)

		data, _, err := client.Get(
			ctx,
			"/v1/events",
			query,
		)
		if err != nil {
			if ctx.Err() != nil {
				return
			}

			fmt.Fprintln(
				os.Stderr,
				"watch request failed:",
				err,
			)

			if !sleepContext(ctx, *interval) {
				return
			}

			continue
		}

		var page eventPage

		if err := json.Unmarshal(data, &page); err != nil {
			fatal(
				fmt.Errorf(
					"decode event page: %w",
					err,
				),
			)
		}

		for _, raw := range page.Items {
			if *jsonOutput {
				printCompactJSON(raw)
				continue
			}

			printEventLine(raw)
		}

		if page.NextSequence > currentSequence {
			currentSequence = page.NextSequence
		}

		if !sleepContext(ctx, *interval) {
			return
		}
	}
}

func eventQuery(
	since uint64,
	limit int,
	kind string,
	agentID string,
	phase string,
) url.Values {
	query := url.Values{}

	query.Set(
		"since",
		strconv.FormatUint(since, 10),
	)

	query.Set(
		"limit",
		strconv.Itoa(limit),
	)

	if strings.TrimSpace(kind) != "" {
		query.Set("kind", strings.TrimSpace(kind))
	}

	if strings.TrimSpace(agentID) != "" {
		query.Set(
			"agent_id",
			strings.TrimSpace(agentID),
		)
	}

	if strings.TrimSpace(phase) != "" {
		query.Set(
			"phase",
			strings.ToUpper(
				strings.TrimSpace(phase),
			),
		)
	}

	return query
}

func runJSON(
	ctx context.Context,
	client *controlclient.Client,
	path string,
	query url.Values,
) {
	data, _, err := client.Get(ctx, path, query)
	if err != nil {
		fatal(err)
	}

	printPrettyJSON(data)
}

func runText(
	ctx context.Context,
	client *controlclient.Client,
	path string,
	query url.Values,
) {
	data, _, err := client.Get(ctx, path, query)
	if err != nil {
		fatal(err)
	}

	fmt.Print(string(data))
}

func printPrettyJSON(data []byte) {
	var output bytes.Buffer

	if err := json.Indent(
		&output,
		bytes.TrimSpace(data),
		"",
		"  ",
	); err != nil {
		fatal(
			fmt.Errorf(
				"format JSON response: %w",
				err,
			),
		)
	}

	fmt.Println(output.String())
}

func printCompactJSON(data []byte) {
	var output bytes.Buffer

	if err := json.Compact(
		&output,
		data,
	); err != nil {
		fatal(
			fmt.Errorf(
				"format event JSON: %w",
				err,
			),
		)
	}

	fmt.Println(output.String())
}

func printEventLine(data []byte) {
	var event struct {
		Sequence  uint64    `json:"sequence"`
		Timestamp time.Time `json:"timestamp"`
		Kind      string    `json:"kind"`
		AgentID   string    `json:"agent_id"`
		Phase     string    `json:"phase"`
	}

	if err := json.Unmarshal(data, &event); err != nil {
		printCompactJSON(data)
		return
	}

	agentID := event.AgentID
	if agentID == "" {
		agentID = "-"
	}

	phase := event.Phase
	if phase == "" {
		phase = "-"
	}

	fmt.Printf(
		"%06d  %s  %-42s  %-28s  %s\n",
		event.Sequence,
		event.Timestamp.Format(
			time.RFC3339Nano,
		),
		event.Kind,
		agentID,
		phase,
	)
}

func sleepContext(
	ctx context.Context,
	duration time.Duration,
) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false

	case <-timer.C:
		return true
	}
}

func environmentOrDefault(
	name string,
	fallback string,
) string {
	value := strings.TrimSpace(
		os.Getenv(name),
	)

	if value == "" {
		return fallback
	}

	return value
}

func usage() {
	fmt.Fprintln(
		os.Stderr,
		`Usage:
  capsulectl [global options] <command> [command options]

Global options:
  -server URL       Runtime API URL
  -timeout DURATION HTTP timeout

Commands:
  health
  ready
  status
  agents [-phase PHASE] [-limit N]
  agent <agent-id>
  events [-since N] [-limit N] [-kind KIND] [-agent-id ID] [-phase PHASE]
  watch [-since N] [-interval 1s] [-kind KIND] [-agent-id ID] [-phase PHASE] [-json]
  metrics

Environment:
  CAPSULERT_SERVER  Default Runtime API URL
  AEGISRT_SERVER    Legacy compatibility alias

Examples:
  capsulectl status
  capsulectl agents -phase FAILED
  capsulectl agent api-producer-success
  capsulectl events -since 10
  capsulectl watch -agent-id api-producer-success
  capsulectl metrics`,
	)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "capsulectl:", err)
	os.Exit(1)
}
