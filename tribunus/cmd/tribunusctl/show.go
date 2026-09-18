package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/cordanaLLM/praetor/tribunus/catalog"
)

// maxSnapshotReadBytes bounds how large a snapshot file show will read
// (HISS-02).
const maxSnapshotReadBytes = 64 << 20

// runShow is tribunusctl's "show" subcommand: it reads a snapshot file
// written by sync and renders it as a table.
func runShow(args []string) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	in := fs.String("in", defaultSnapshotPath, "path to a JSON snapshot written by sync")
	if err := fs.Parse(args); err != nil {
		return err
	}

	snap, err := readSnapshotFile(*in)
	if err != nil {
		return err
	}
	renderTable(os.Stdout, snap)
	return nil
}

func readSnapshotFile(path string) (catalog.Snapshot, error) {
	f, err := os.Open(path)
	if err != nil {
		return catalog.Snapshot{}, fmt.Errorf("open snapshot %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return catalog.Snapshot{}, fmt.Errorf("stat snapshot %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return catalog.Snapshot{}, fmt.Errorf("snapshot %s is not a regular file", path)
	}
	if info.Size() > maxSnapshotReadBytes {
		return catalog.Snapshot{}, fmt.Errorf("snapshot %s exceeds %d bytes", path, maxSnapshotReadBytes)
	}

	data, err := io.ReadAll(io.LimitReader(f, maxSnapshotReadBytes+1))
	if err != nil {
		return catalog.Snapshot{}, fmt.Errorf("read snapshot %s: %w", path, err)
	}
	if len(data) > maxSnapshotReadBytes {
		return catalog.Snapshot{}, fmt.Errorf("snapshot %s exceeds %d bytes", path, maxSnapshotReadBytes)
	}

	snap, err := catalog.ParseSnapshot(data)
	if err != nil {
		return catalog.Snapshot{}, fmt.Errorf("parse snapshot %s: %w", path, err)
	}
	return snap, nil
}

// renderTable prints one row per record: id, access path, context window,
// prices, and provenance. Absent values print as "-", never a fabricated
// number.
func renderTable(w io.Writer, snap catalog.Snapshot) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "MODEL\tACCESS\tCTX\tIN/M\tOUT/M\tSOURCE\tKIND")
	for _, r := range snap.Records {
		fmt.Fprintln(tw, strings.Join([]string{
			r.ModelID,
			string(r.AccessPath),
			formatInt64Ptr(r.ContextWindow),
			formatFloatPtr(r.PriceInPerM),
			formatFloatPtr(r.PriceOutPerM),
			r.Provenance.Source,
			string(r.Provenance.Kind),
		}, "\t"))
	}
	_ = tw.Flush()
	fmt.Fprintf(w, "\n%d records, generated %s\n", len(snap.Records), snap.GeneratedAt.Format("2006-01-02T15:04:05Z07:00"))
	for _, run := range snap.SourceRuns {
		line := fmt.Sprintf("  %-16s %-4s count=%d", run.Source, run.Status, run.Count)
		if run.Detail != "" {
			line += " " + run.Detail
		}
		fmt.Fprintln(w, line)
	}
}

func formatInt64Ptr(v *int64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%d", *v)
}

func formatFloatPtr(v *float64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%.4g", *v)
}
