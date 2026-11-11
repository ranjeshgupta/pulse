package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"

	"pulse/internal/channel"
	"pulse/internal/kafka"
)

// replayTarget turns the replay-dlq argument into the topic to replay into: a topic
// ("messages.email"), its dead-letter topic ("messages.email.dlq") or a channel ("email").
func replayTarget(arg string) (string, error) {
	t := strings.TrimSuffix(strings.TrimSpace(arg), ".dlq")
	if slices.Contains(channel.All, t) {
		t = channel.Topic(t)
	}
	if !slices.Contains(pipelineTopics(), t) {
		return "", fmt.Errorf("unknown topic %q: use one of %s, or a channel name", arg, strings.Join(pipelineTopics(), ", "))
	}
	return t, nil
}

// runReplayDLQ moves records from a dead-letter topic back to its topic (see kafka.Replay):
//
//	pulse replay-dlq [--limit N] [--dry-run] <topic or channel>
func runReplayDLQ(ctx context.Context, d *deps) error {
	fs := flag.NewFlagSet("replay-dlq", flag.ContinueOnError)
	limit := fs.Int("limit", 0, "replay at most this many records (0: all)")
	dry := fs.Bool("dry-run", false, "list the records without replaying them")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: pulse replay-dlq [--limit N] [--dry-run] <topic or channel, e.g. messages.email or email>")
		fs.PrintDefaults()
	}
	args := os.Args[2:]
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		fs.Usage()
		return fmt.Errorf("replay-dlq needs a topic")
	}
	arg := fs.Arg(0)
	if err := fs.Parse(fs.Args()[1:]); err != nil { // flags after the topic
		return err
	}
	topic, err := replayTarget(arg)
	if err != nil {
		return err
	}
	prod, err := d.producer()
	if err != nil {
		return err
	}
	n, err := kafka.Replay(ctx, d.cfg.KafkaBrokers, topic, prod, d.log,
		kafka.ReplayOptions{Limit: *limit, DryRun: *dry, Out: os.Stdout})
	if *dry {
		d.log.Info("dry run: nothing replayed", "topic", topic+".dlq", "records", n)
	} else {
		d.log.Info("replay finished", "topic", topic, "records", n)
	}
	return err
}
