package devsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// metadataTimeout bounds every rclone call that moves no archive data.
	metadataTimeout = 2 * time.Minute
	// transferTimeout bounds one archive upload or download.
	transferTimeout = 6 * time.Hour
	// maxRcloneOutput caps captured rclone stdout and stderr per stream.
	maxRcloneOutput = 16 << 20
	// maxRemoteArchives bounds a remote listing (HISS-02).
	maxRemoteArchives = 10000
	// rcloneDirNotFound and rcloneFileNotFound are rclone's documented exit codes.
	rcloneDirNotFound  = 3
	rcloneFileNotFound = 4
)

// ErrRemoteMissing reports a remote path that holds nothing yet.
var ErrRemoteMissing = errors.New("devsync: remote path does not exist")

// Rclone runs the rclone binary through the audited command runner.
type Rclone struct {
	// Binary is the rclone executable; empty selects "rclone" from PATH. Tests set a stub.
	Binary string
	// Config is passed to rclone as --config when set; empty keeps rclone's own default.
	Config string
}

// RemoteArchive is one archive stored on the remote.
type RemoteArchive struct {
	Host    string    `json:"host"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

func (r Rclone) binary() string {
	if r.Binary == "" {
		return "rclone"
	}
	return r.Binary
}

func (r Rclone) argv(args []string) []string {
	if r.Config == "" {
		return args
	}
	return append([]string{"--config", r.Config}, args...)
}

func (r Rclone) run(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, metadataTimeout)
	defer cancel()
	result, err := util.RunCommandBytes(ctx, "", r.binary(), maxRcloneOutput, r.argv(args)...)
	if err != nil {
		return nil, rcloneError(args[0], err, result.Stderr)
	}
	return result.Stdout, nil
}

func (r Rclone) stream(ctx context.Context, stdin io.Reader, stdout io.Writer, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, transferTimeout)
	defer cancel()
	stderr, err := util.RunCommandStream(ctx, "", r.binary(), stdin, stdout, maxRcloneOutput, r.argv(args)...)
	if err != nil {
		return rcloneError(args[0], err, stderr)
	}
	return nil
}

func rcloneError(verb string, err error, stderr []byte) error {
	excerpt := strings.TrimSpace(util.TruncateExcerpt(string(stderr), 512))
	if excerpt == "" {
		return fmt.Errorf("rclone %s: %w", verb, err)
	}
	return fmt.Errorf("rclone %s: %w: %s", verb, err, excerpt)
}

// exitCode returns rclone's exit status, or -1 when the command did not run to an exit.
func exitCode(err error) int {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return -1
}

// remotePath joins slash-separated parts onto remote, which ends in ":" for a named remote
// or is a plain directory path.
func remotePath(remote string, parts ...string) string {
	joined := strings.Join(parts, "/")
	if strings.HasSuffix(remote, ":") || strings.HasSuffix(remote, "/") {
		return remote + joined
	}
	return remote + "/" + joined
}

// validateRemotePath refuses a remote path rclone could read as a flag or that carries a
// shell metacharacter or control character. No shell is involved; the rule keeps every
// argument handed to a subprocess inside the repository's audited exec contract.
func validateRemotePath(p string) error {
	if err := util.ValidateExecPathArg(p); err != nil {
		return fmt.Errorf("remote path %q: %w", p, err)
	}
	return nil
}

// listRemotes returns the names of the remotes in the rclone configuration.
func (r Rclone) listRemotes(ctx context.Context) ([]string, error) {
	out, err := r.run(ctx, "listremotes", "--json")
	if err != nil {
		return nil, err
	}
	var remotes []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(out, &remotes); err != nil {
		return nil, fmt.Errorf("decode rclone listremotes: %w", err)
	}
	names := make([]string, 0, len(remotes))
	for i := 0; i < len(remotes) && i < maxRemoteArchives; i++ {
		names = append(names, remotes[i].Name)
	}
	return names, nil
}

// listArchives lists every archive below dir. A directory that does not exist yet returns
// ErrRemoteMissing so callers can tell "empty" from "unreachable".
func (r Rclone) listArchives(ctx context.Context, dir string) ([]RemoteArchive, error) {
	if err := validateRemotePath(dir); err != nil {
		return nil, err
	}
	out, err := r.run(ctx, "lsjson", "--recursive", "--files-only", dir)
	if exitCode(err) == rcloneDirNotFound {
		return nil, fmt.Errorf("%w: %s", ErrRemoteMissing, dir)
	}
	if err != nil {
		return nil, err
	}
	// Field names follow rclone's lsjson output.
	var entries []struct {
		Path    string
		Size    int64
		ModTime time.Time
	}
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Errorf("decode rclone lsjson: %w", err)
	}
	if len(entries) > maxRemoteArchives {
		return nil, fmt.Errorf("remote %s lists %d files, more than %d", dir, len(entries), maxRemoteArchives)
	}
	archives := make([]RemoteArchive, 0, len(entries))
	for _, entry := range entries {
		if strings.HasSuffix(entry.Path, archiveSuffix) {
			archives = append(archives, RemoteArchive{Path: entry.Path, Size: entry.Size, ModTime: entry.ModTime})
		}
	}
	sort.Slice(archives, func(i, j int) bool { return archives[i].Path < archives[j].Path })
	return archives, nil
}

// countingWriter counts the bytes written through it.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// upload streams produce's output to dest. It writes dest+".partial" first and moves it into
// place only after a complete upload, so a failed push never replaces the last good archive.
func (r Rclone) upload(ctx context.Context, dest string, produce func(io.Writer) error) (int64, error) {
	if err := validateRemotePath(dest); err != nil {
		return 0, err
	}
	partial := dest + partialSuffix
	reader, writer := io.Pipe()
	counter := &countingWriter{w: writer}
	done := make(chan error, 1)
	go func() {
		err := produce(counter)
		done <- errors.Join(err, writer.CloseWithError(err))
	}()
	streamErr := r.stream(ctx, reader, nil, "rcat", partial)
	closeErr := reader.CloseWithError(io.ErrClosedPipe) // releases the producer if rclone stopped reading
	if cause := uploadCause(<-done, streamErr); cause != nil {
		return 0, errors.Join(cause, closeErr, r.removeIfPresent(context.WithoutCancel(ctx), partial))
	}
	if _, err := r.run(ctx, "moveto", partial, dest); err != nil {
		return 0, errors.Join(err, r.removeIfPresent(context.WithoutCancel(ctx), partial))
	}
	return counter.n, closeErr
}

// uploadCause names the root cause of a failed upload. A producer that only saw the pipe
// close was stopped by rclone failing, so rclone's error is the one worth reporting.
func uploadCause(produceErr, streamErr error) error {
	if produceErr != nil && !errors.Is(produceErr, io.ErrClosedPipe) {
		return produceErr
	}
	if streamErr != nil {
		return streamErr
	}
	return produceErr
}

func (r Rclone) removeIfPresent(ctx context.Context, path string) error {
	_, err := r.run(ctx, "deletefile", path)
	if code := exitCode(err); code == rcloneDirNotFound || code == rcloneFileNotFound {
		return nil
	}
	return err
}

// download streams src into consume. When consume fails, rclone is stopped at once rather
// than left writing into a pipe nobody reads.
func (r Rclone) download(ctx context.Context, src string, consume func(io.Reader) error) error {
	if err := validateRemotePath(src); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := consume(reader)
		if err != nil {
			cancel()
		}
		done <- errors.Join(err, reader.CloseWithError(err))
	}()
	streamErr := r.stream(ctx, nil, writer, "cat", src)
	closeErr := writer.CloseWithError(streamErr)
	consumeErr := <-done
	if consumeErr != nil && (streamErr == nil || ctx.Err() != nil) {
		return consumeErr // the consumer failed first and stopped rclone
	}
	return errors.Join(streamErr, closeErr)
}
