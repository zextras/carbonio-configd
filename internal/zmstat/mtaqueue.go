// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package zmstat

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/zextras/carbonio-configd/internal/sys"
)

const mtaqueueUsage = `Usage: zmstat-mtaqueue [options]
Monitor MTA queue
-i, --interval: output a line every N seconds
-l, --log:      log file (default is /opt/zextras/zmstat/mtaqueue.csv)
-c, --console:  output to stdout
--max-qsize:    max queue size at which to start backing off mointor interval (default 1000)
--backoff-interval: how many seconds to backoff monitor schedule if queue hits --max-qsize (default 30 seconds)
--max-interval: maximum monitor interval (default 600 seconds)

If logging to a file, rotation occurs when HUP signal is sent or when
date changes.  Current log is renamed to <dir>/YYYY-MM-DD/mtaqueue.csv
and a new file is created.
`

// queueTotal is the last line of "postqueue -p".
var queueTotal = regexp.MustCompile(`(\d+) Kbytes in (\d+) Requests`)

func mtaqueueSpec() *spec {
	return &spec{
		name:  "mtaqueue",
		files: fixedFiles("mtaqueue.csv", "mtaqueue.pid"),
		opts:  []optDef{{"max-qsize", kindInt}, {"backoff-interval", kindInt}, {"max-interval", kindInt}},
		usage: mtaqueueUsage,
		build: func(env *Env, o *options) (Collector, error) {
			secs := func(key string, def int) time.Duration {
				if v, ok := o.ints[key]; ok {
					return time.Duration(v) * time.Second
				}

				return time.Duration(def) * time.Second
			}

			q := &mtaqueue{
				env: env, base: o.interval, cur: o.interval,
				backoff: secs("backoff-interval", 30), maxInterval: secs("max-interval", 600), maxQSize: 1000,
			}
			if v, ok := o.ints["max-qsize"]; ok {
				q.maxQSize = v
			}

			return q, nil
		},
	}
}

// mtaqueue is stat-mtaqueue.pl: the size of the mail queue from postqueue,
// sampled less often while the queue is bigger than maxQSize.
type mtaqueue struct {
	env         *Env
	base, cur   time.Duration
	maxQSize    int
	backoff     time.Duration
	maxInterval time.Duration
}

// Heading implements Collector.
func (q *mtaqueue) Heading() string { return "timestamp, KBytes, requests" }

// Interval implements Paced.
func (q *mtaqueue) Interval() time.Duration { return q.cur }

// Sample implements Collector.
func (q *mtaqueue) Sample(t Tick) ([]string, error) {
	kb, msgs := q.queue()

	q.prometheus()
	q.adapt(t.Stamp, msgs)

	return []string{fmt.Sprintf("%s, %s, %s", t.Stamp, kb, msgs)}, nil
}

// queue is getQueueStat: kilobytes and requests, 0 and 0 when postqueue does
// not say.
func (q *mtaqueue) queue() (kb, msgs string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	prog := filepath.Join(q.env.Base, "common", "sbin", "postqueue")
	if _, err := os.Stat(prog); err != nil {
		prog = "postqueue"
	}

	// The exit status does not matter: the Perl reads the end of a pipe.
	res, _ := q.env.Sys.Exec.Run(ctx, &sys.Cmd{Path: prog, Args: []string{"-p"}})

	out := strings.TrimSuffix(string(res.Stdout), "\n")
	if i := strings.LastIndexByte(out, '\n'); i >= 0 {
		out = out[i+1:]
	}

	if m := queueTotal.FindStringSubmatch(out); m != nil {
		return m[1], m[2]
	}

	return "0", "0"
}

// prometheus starts libexec/postfix-prometheus in the background, as the Perl
// does on every sample, to refresh the postfix metrics.
func (q *mtaqueue) prometheus() {
	p, err := q.env.Sys.Exec.Start(context.Background(), &sys.Cmd{
		Path: filepath.Join(q.env.Base, "libexec", "postfix-prometheus"),
	})
	if err != nil {
		return
	}

	go func() { _ = p.Wait() }()
}

// adapt moves the interval: up by the backoff step while the queue is over the
// limit (to the maximum), back to the base one when it is not.
func (q *mtaqueue) adapt(stamp, msgs string) {
	var n int

	_, _ = fmt.Sscanf(msgs, "%d", &n)

	switch {
	case n > q.maxQSize && q.cur < q.maxInterval:
		q.cur = min(q.cur+q.backoff, q.maxInterval)
		_, _ = fmt.Fprintf(q.env.Stderr, "%s: Queue is too big! (%d msgs); Increasing monitor interval to %d seconds\n",
			stamp, n, int(q.cur.Seconds()))
	case n > q.maxQSize:
		_, _ = fmt.Fprintf(q.env.Stderr,
			"%s: Queue is too big! (%d msgs); Already at maximum monitor interval of %d seconds\n",
			stamp, n, int(q.cur.Seconds()))
	case q.cur > q.base:
		q.cur = q.base
		_, _ = fmt.Fprintf(q.env.Stderr,
			"%s: Queue size is back within the maximum of %d; Restoring default monitor interval of %d seconds\n",
			stamp, q.maxQSize, int(q.base.Seconds()))
	}
}
