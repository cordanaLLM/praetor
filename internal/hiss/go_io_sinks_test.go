// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"strings"
	"testing"
)

// Sinks (#841, pattern 2): the log/slog functions and *slog.Logger methods take a context only
// for its values, and a select over channels reads it through ctx.Done(). Neither is I/O on it.

// Negative: package functions, a logger parameter, logger locals built by log/slog, With chains,
// a typed var, a receiver field declared *slog.Logger in another file of the package, and a
// select whose cases only touch channels stay silent.
func TestGoIOSinks_Negative_LoggerSinksAndChannelSelects(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "svc/types.go", strings.Join([]string{
		"package svc",
		"",
		"import \"log/slog\"",
		"",
		"type Server struct {",
		"\tlog  *slog.Logger",
		"\tname string",
		"}",
		"",
	}, "\n"))
	writeFixture(t, root, "svc/svc.go", strings.Join([]string{
		"package svc",
		"",
		"import (",
		"\t\"context\"",
		"\tlog \"log/slog\"",
		")",
		"",
		"func Run(logger *log.Logger, out chan<- int, in <-chan int) {",
		"\tctx := context.Background()",
		"\tlog.InfoContext(ctx, \"start\")",
		"\tlog.Log(ctx, log.LevelInfo, \"start\")",
		"\tlogger.ErrorContext(ctx, \"x\")",
		"\tlogger.With(\"k\", 1).WithGroup(\"g\").WarnContext(ctx, \"x\")",
		"\tl := log.New(nil)",
		"\tl.DebugContext(ctx, \"x\")",
		"\tlog.Default().LogAttrs(ctx, log.LevelInfo, \"x\")",
		"\tvar typed *log.Logger",
		"\ttyped.InfoContext(ctx, \"x\")",
		"\tgo func() { logger.InfoContext(ctx, \"in a goroutine\") }()",
		"\tselect {",
		"\tcase out <- 1:",
		"\tcase v := <-in:",
		"\t\t_ = v",
		"\tcase <-ctx.Done():",
		"\t}",
		"}",
		"",
		"func (s *Server) Serve() {",
		"\tctx := context.Background()",
		"\ts.log.ErrorContext(ctx, \"serve\")",
		"}",
		"",
	}, "\n"))
	if rep := scanFixture(t, root, ScanOptions{}); hiss02Count(rep) != 0 {
		t.Fatalf("log/slog sinks and channel selects do no I/O on the context: %+v", rep.Violations)
	}
}

// Positive: the near-misses stay reported. A sink method on a receiver the walk cannot prove a
// *slog.Logger, a Log method of another type, a field that is not *slog.Logger, a receiver field
// reached through a shadowing local, a shadowed log/slog package, and a select one of whose cases
// performs a blocking call with the context.
func TestGoIOSinks_Positive_UnprovenReceiversAndBlockingSelects(t *testing.T) {
	src := strings.Join([]string{
		"package svc", // 1
		"",            // 2
		"import (",    // 3
		"\t\"context\"",
		"\t\"log/slog\"",
		")",                     // 6
		"",                      // 7
		"type Server struct {",  // 8
		"\tlog    *slog.Logger", // 9
		"\tremote Shipper",      // 10
		"}",                     // 11
		"",                      // 12
		"func (s *Server) Run(client Client, out chan<- []byte) {", // 13
		"\tctx := context.Background()",                            // 14
		"\tclient.ErrorContext(ctx, \"x\")",                        // 15 receiver unproven
		"\tclient.Log(ctx, entry)",                                 // 16 another type's Log
		"\ts.remote.ErrorContext(ctx, \"x\")",                      // 17 field is not a logger
		"\tfunc(s *Other) { s.log.InfoContext(ctx, \"x\") }(nil)",  // 18 s is the literal's parameter of a type with no logger
		"\tslog := client",                                         // 19
		"\tslog.InfoContext(ctx, \"x\")",                           // 20 shadowed package
		"\tselect {",                                               // 21
		"\tcase out <- client.Fetch(ctx):",                         // 22 a case that does I/O
		"\tcase <-ctx.Done():",                                     // 23
		"\t}",                                                      // 24
		"}",                                                        // 25
		"",
	}, "\n")
	rep := scanGoIO(t, "svc.go", src)
	assertViolations(t, rep, []expectedViolation{
		{"HISS-02", "svc.go", 15}, {"HISS-02", "svc.go", 16}, {"HISS-02", "svc.go", 17},
		{"HISS-02", "svc.go", 18}, {"HISS-02", "svc.go", 20}, {"HISS-02", "svc.go", 22},
	})
}

// Boundary: a logger local goes out of scope with its block, a logger rebound to something else
// is no longer one, and a struct type declared twice in a package (two build-tagged files) proves
// no field.
func TestGoIOSinks_Boundary_ScopesAndAmbiguousTypes(t *testing.T) {
	src := strings.Join([]string{
		"package svc", // 1
		"",            // 2
		"import (",    // 3
		"\t\"context\"",
		"\t\"log/slog\"",
		")",                                   // 6
		"",                                    // 7
		"func Run(cond bool, other Client) {", // 8
		"\tctx := context.Background()",       // 9
		"\tif cond {",                         // 10
		"\t\tl := slog.Default()",             // 11
		"\t\tl.InfoContext(ctx, \"x\")",       // 12 silent
		"\t}",                                 // 13
		"\tl.InfoContext(ctx, \"x\")",         // 14 another l
		"\tm := slog.Default()",               // 15
		"\tm = other",                         // 16
		"\tm.InfoContext(ctx, \"x\")",         // 17 rebound
		"}",                                   // 18
		"",
	}, "\n")
	assertViolations(t, scanGoIO(t, "svc.go", src), []expectedViolation{
		{"HISS-02", "svc.go", 14}, {"HISS-02", "svc.go", 17},
	})
	root := t.TempDir()
	server := "package svc\n\nimport \"log/slog\"\n\ntype Server struct{ log *slog.Logger }\n"
	writeFixture(t, root, "svc/server_linux.go", server)
	writeFixture(t, root, "svc/server_other.go", strings.Replace(server, "*slog.Logger", "Shipper", 1))
	writeFixture(t, root, "svc/run.go", strings.Join([]string{
		"package svc",        // 1
		"",                   // 2
		"import \"context\"", // 3
		"",                   // 4
		"func (s *Server) Run() { s.log.InfoContext(context.TODO(), \"x\") }", // 5
		"",
	}, "\n"))
	assertViolations(t, scanFixture(t, root, ScanOptions{}), []expectedViolation{{"HISS-02", "svc/run.go", 5}})
}
