// SPDX-License-Identifier: AGPL-3.0-or-later

package ops

import (
	"runtime"
	"time"

	"github.com/some-du6e/pairtime/internal/version"
)

// RowBuckets bound the rows of one writer transaction; MaxRows is 500.
var RowBuckets = []float64{1, 5, 10, 50, 100, 250, 500, 1000, 2500}

// Series are the metrics DESIGN 10.4 names, plus the writer's own. The
// ingest and stream series stay at zero until WP-SV4 and WP-SV8 count
// them; Litestream lag comes with WP-OP1.
type Series struct {
	// IngestBatches counts accepted ingest batches; its rate is the
	// ingest rate.
	IngestBatches *Counter
	// IngestBatchSeconds is ingest batch latency, from request to receipt.
	IngestBatchSeconds *Histogram
	// StreamsOpen is the number of open SSE streams.
	StreamsOpen *Gauge

	// WriterCommits counts writer transactions, and WriterCommitErrors
	// the ones that failed as a whole.
	WriterCommits, WriterCommitErrors *Counter
	// WriterOpErrors counts ops rolled back alone inside a transaction.
	WriterOpErrors *Counter
	// WriterCommitSeconds is how long each transaction took.
	WriterCommitSeconds *Histogram
	// WriterBatchRows is how many rows each transaction held.
	WriterBatchRows *Histogram
}

// NewSeries registers the series on reg. queueDepth reads the writer's
// queue depth at each scrape.
func NewSeries(reg *Registry, queueDepth func() int) *Series {
	s := &Series{
		IngestBatches:       reg.Counter("pairtime_ingest_batches_total", "Ingest batches accepted. Its rate is the ingest rate."),
		IngestBatchSeconds:  reg.Histogram("pairtime_ingest_batch_duration_seconds", "Time from an ingest request to its receipt.", DurationBuckets),
		StreamsOpen:         reg.Gauge("pairtime_stream_open_streams", "Open live-dashboard streams (SSE)."),
		WriterCommits:       reg.Counter("pairtime_writer_commits_total", "Transactions the writer goroutine finished."),
		WriterCommitErrors:  reg.Counter("pairtime_writer_commit_errors_total", "Writer transactions that failed as a whole."),
		WriterOpErrors:      reg.Counter("pairtime_writer_op_errors_total", "Writes rolled back alone inside a writer transaction."),
		WriterCommitSeconds: reg.Histogram("pairtime_writer_commit_duration_seconds", "Time from BEGIN to the end of COMMIT for one writer transaction.", DurationBuckets),
		WriterBatchRows:     reg.Histogram("pairtime_writer_batch_rows", "Rows in one writer transaction.", RowBuckets),
	}
	reg.GaugeFunc("pairtime_writer_queue_depth", "Writes handed to the writer goroutine and not yet finished.",
		func() float64 { return float64(queueDepth()) })
	reg.GaugeFunc("go_goroutines", "Goroutines that currently exist.",
		func() float64 { return float64(runtime.NumGoroutine()) })
	start := float64(time.Now().UnixMilli()) / 1000
	reg.GaugeFunc("process_start_time_seconds", "Start time of the process since the Unix epoch, in seconds.",
		func() float64 { return start })
	v := version.Get()
	reg.Info("pairtime_build_info", "The running build.", map[string]string{
		"version": v.Version, "commit": v.Commit, "go_version": v.GoVersion,
	})
	return s
}

// ObserveCommit records one writer transaction.
func (s *Series) ObserveCommit(d time.Duration, rows, failedOps int, err error) {
	s.WriterCommits.Inc()
	if err != nil {
		s.WriterCommitErrors.Inc()
	}
	s.WriterOpErrors.Add(uint64(failedOps))
	s.WriterCommitSeconds.Observe(d.Seconds())
	s.WriterBatchRows.Observe(float64(rows))
}
