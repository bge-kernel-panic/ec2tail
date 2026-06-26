// Command ec2tail tails plaintext log files across a set of EC2 instances (selected by tag) and
// interleaves their lines into one prefixed, color-coded stream — "stern for EC2". It uses AWS SSM
// Session Manager over the SDK; no SSH, no session-manager-plugin, no aws CLI.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// tag is a single key=value instance filter.
type tag struct {
	key   string
	value string
}

// tagList collects repeated --tag flags.
type tagList []tag

func (t *tagList) String() string { return fmt.Sprintf("%v", []tag(*t)) }

func (t *tagList) Set(v string) error {
	key, value, ok := strings.Cut(v, "=")
	if !ok || key == "" {
		return fmt.Errorf("tag must be key=value, got %q", v)
	}
	*t = append(*t, tag{key: key, value: value})
	return nil
}

func main() {
	os.Exit(run())
}

func run() int {
	var tags tagList
	flag.Var(&tags, "tag", "instance filter key=value (repeatable, AND-combined; at least one required)")
	flag.Usage = usage
	flag.Parse()

	globs := flag.Args()
	if len(tags) == 0 || len(globs) == 0 {
		usage()
		return 2
	}

	// The library logs protocol noise (e.g. "WriteTo read error: EOF" on normal close) to the std
	// logger. Keep our output clean; we report everything user-facing ourselves.
	log.SetOutput(io.Discard)

	// SIGINT/SIGTERM cancel the context; a watcher then tears down every live session, which
	// unblocks the WriteTo read loops so the goroutines can exit.
	ctx, stop := notifyContext(context.Background())
	defer stop()

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ec2tail: failed to load AWS config: %v\n", err)
		return 1
	}

	instances, err := discover(ctx, ec2.NewFromConfig(cfg), tags)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ec2tail: %v\n", err)
		return 1
	}
	if len(instances) == 0 {
		fmt.Fprintln(os.Stderr, "ec2tail: no running instances matched the given tags")
		return 1
	}
	// Print the count before connecting, giving a Ctrl-C window.
	fmt.Fprintf(os.Stderr, "ec2tail: connecting to %d instance(s)...\n", len(instances))

	hosts := buildHosts(instances)

	out := make(chan outMsg, 256)
	writerDone := make(chan struct{})
	go writer(out, writerDone)

	reg := &registry{}
	go func() {
		<-ctx.Done()
		fmt.Fprintln(os.Stderr, "ec2tail: shutting down...")
		reg.teardownAll()
	}()

	var ran int32
	var wg sync.WaitGroup
	for i := range instances {
		wg.Add(1)
		go func(h *host, inst instance) {
			defer wg.Done()
			streamHost(ctx, cfg, h, inst, globs, out, reg, &ran)
		}(hosts[i], instances[i])
	}
	wg.Wait()

	close(out)
	<-writerDone

	// Layer-2 sweep with a fresh context: the main one may already be cancelled by the signal.
	sweepCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sweepOrphans(sweepCtx, ssm.NewFromConfig(cfg), instances)

	if atomic.LoadInt32(&ran) == 0 {
		return 1
	}
	return 0
}

// writer owns stdout/stderr so complete lines never interleave mid-line across hosts.
func writer(out <-chan outMsg, done chan<- struct{}) {
	for msg := range out {
		if msg.isErr {
			fmt.Fprintln(os.Stderr, msg.text)
		} else {
			fmt.Fprintln(os.Stdout, msg.text)
		}
	}
	close(done)
}

func incr(n *int32) { atomic.AddInt32(n, 1) }

func usage() {
	fmt.Fprintf(os.Stderr, `ec2tail — tail log files across EC2 instances selected by tag

Usage:
  ec2tail --tag key=value [--tag key=value ...] '<glob>' ['<glob>' ...]

Flags:
  --tag key=value   instance filter (repeatable, AND-combined; at least one required)

Arguments:
  one or more remote file paths/globs (quote them so the local shell does not expand them)

AWS region, profile, and credentials are taken from the environment.

Example:
  ec2tail --tag app=web --tag env=prod '/var/log/app/*.log'
`)
}
