package main

import (
	"context"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// sweepOrphans is the belt-and-suspenders cleanup layer. SsmDataChannel does not expose its
// SessionId, so we cannot terminate by id directly; instead we list active sessions, keep the
// ones targeting our instances, and terminate them. This also reaps orphans left behind by a
// previous run that was SIGKILLed before its native teardown could run.
func sweepOrphans(ctx context.Context, client *ssm.Client, instances []instance) {
	targets := make(map[string]bool, len(instances))
	for _, inst := range instances {
		targets[inst.id] = true
	}

	paginator := ssm.NewDescribeSessionsPaginator(client, &ssm.DescribeSessionsInput{
		State: ssmtypes.SessionStateActive,
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cleanup: DescribeSessions failed: %v\n", err)
			return
		}
		for _, sess := range page.Sessions {
			if !targets[aws.ToString(sess.Target)] {
				continue
			}
			if _, err := client.TerminateSession(ctx, &ssm.TerminateSessionInput{
				SessionId: sess.SessionId,
			}); err != nil {
				fmt.Fprintf(os.Stderr, "cleanup: failed to terminate session %s: %v\n",
					aws.ToString(sess.SessionId), err)
			}
		}
	}
}
