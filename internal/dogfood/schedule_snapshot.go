package dogfood

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

type scheduleSnapshot struct {
	config      *ScheduleConfig
	files       map[string][]byte
	fingerprint string
	runnerSHA   string
}

func loadScheduleSnapshot(ctx context.Context, path string) (*scheduleSnapshot, error) {
	return loadScheduleSnapshotWithinRoot(ctx, path, "")
}

func loadScheduleSnapshotWithinRoot(ctx context.Context, path, inputRoot string) (*scheduleSnapshot, error) {
	if err := confineSchedulePath(inputRoot, path); err != nil {
		return nil, err
	}
	data, configuration, err := readScheduleConfig(ctx, path)
	if err != nil {
		return nil, err
	}
	if err := confineScheduleConfig(inputRoot, configuration); err != nil {
		return nil, err
	}
	suiteData, err := snapshotScheduleSuite(ctx, configuration, inputRoot)
	if err != nil {
		return nil, err
	}
	snapshot := &scheduleSnapshot{config: configuration, files: map[string][]byte{"schedule.json": data, "suite.json": suiteData}}
	if err := snapshot.captureBundle(ctx); err != nil {
		return nil, err
	}
	if err := snapshot.captureRepairPolicy(ctx); err != nil {
		return nil, err
	}
	executable, err := scheduleConfiguredBinarySHA(ctx, configuration.RunnerBinary)
	if err != nil {
		return nil, err
	}
	manifest := struct {
		Version    int
		Executable string
		Files      map[string][]byte
	}{1, executable, snapshot.files}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	snapshot.fingerprint = hex.EncodeToString(digest[:])
	snapshot.runnerSHA = executable
	return snapshot, nil
}

func (s *scheduleSnapshot) captureBundle(ctx context.Context) (err error) {
	root, err := openSuiteDirectory(ctx, s.config.SourceRoot)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	for _, name := range []string{".standards.yaml", ".standards.lock"} {
		data, err := readScheduleFile(ctx, root, name, 1<<20, false)
		if err != nil {
			return err
		}
		s.files["source/"+name] = data
	}
	var manifest config.Manifest
	if err := yaml.Unmarshal(s.files["source/.standards.yaml"], &manifest); err != nil {
		return err
	}
	if _, err := config.ValidateLockfile(ctx, s.config.SourceRoot, &manifest); err != nil {
		return err
	}
	return s.captureArchetypes(ctx)
}

func (s *scheduleSnapshot) captureArchetypes(ctx context.Context) (err error) {
	path := filepath.Join(s.config.SourceRoot, ".config", "archetypes")
	root, err := openSuiteDirectory(ctx, path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	dirs := []string{"."}
	total := 0
	for i := 0; i < len(dirs) && i < 512; i++ {
		var captureErr error
		dirs, total, captureErr = s.captureArchetypeDirectory(ctx, root, dirs, i, total)
		if captureErr != nil {
			return captureErr
		}
	}
	return ctx.Err()
}

func (s *scheduleSnapshot) materialize(ctx context.Context, root *os.Root, name string) error {
	if err := root.Mkdir(name, 0o700); err != nil {
		return err
	}
	inputs, err := root.OpenRoot(name)
	if err != nil {
		return err
	}
	writeErr := s.writeInputs(ctx, inputs)
	return errors.Join(writeErr, inputs.Close())
}

func (s *scheduleSnapshot) writeInputs(ctx context.Context, root *os.Root) error {
	names := make([]string, 0, len(s.files))
	for name := range s.files {
		names = append(names, name)
	}
	sort.Strings(names)
	for i := 0; i < len(names) && i < 512; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := root.MkdirAll(filepath.Dir(names[i]), 0o700); err != nil {
			return err
		}
		file, err := root.OpenFile(names[i], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		_, writeErr := file.Write(s.files[names[i]])
		if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
			return fmt.Errorf("persist schedule input: %w", err)
		}
	}
	return nil
}

func confineSchedulePath(root, path string) error {
	if root == "" {
		return nil
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	_, err = util.ConfinePath(root, rel)
	return err
}

func confineScheduleConfig(root string, c *ScheduleConfig) error {
	paths := []string{c.SuiteConfig, c.SourceRoot, c.StateDir, c.RunnerBinary}
	if c.RepairPolicy != nil {
		paths = append(paths, c.RepairPolicy.RoutingConfig)
		if c.RepairPolicy.UsagePath != "" {
			paths = append(paths, c.RepairPolicy.UsagePath)
		}
	}
	for i := 0; i < len(paths); i++ {
		if err := confineSchedulePath(root, paths[i]); err != nil {
			return err
		}
	}
	return nil
}

func (s *scheduleSnapshot) captureRepairPolicy(ctx context.Context) error {
	p := s.config.RepairPolicy
	if p == nil {
		return nil
	}
	if err := ValidateRepairPolicy(ctx, *p); err != nil {
		return err
	}
	paths := map[string]string{"routing.json": p.RoutingConfig}
	if p.UsagePath != "" {
		paths["usage.json"] = p.UsagePath
	}
	for name, path := range paths {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return errors.New("repair policy paths must be clean absolute paths")
		}
		root, err := openSuiteDirectory(ctx, filepath.Dir(path))
		if err != nil {
			return err
		}
		data, readErr := readScheduleFile(ctx, root, filepath.Base(path), 1<<20, false)
		if err := errors.Join(readErr, root.Close()); err != nil {
			return err
		}
		s.files[name] = data
	}
	return nil
}

func snapshotScheduleSuite(ctx context.Context, configuration *ScheduleConfig, inputRoot string) ([]byte, error) {
	suite, suiteSum, err := loadSuiteConfig(ctx, configuration.SuiteConfig)
	if err != nil {
		return nil, err
	}
	if err := validateSuitePolicy(suite, SuiteOptions{SourceRoot: configuration.SourceRoot, Stage: "verify", AllowRemote: configuration.AllowRemote, InputRoot: inputRoot}); err != nil {
		return nil, err
	}
	suiteData, err := readSchedulePrivate(ctx, configuration.SuiteConfig)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(suiteData)
	if hex.EncodeToString(sum[:]) != suiteSum {
		return nil, errors.New("suite changed while snapshotting")
	}

	return suiteData, nil
}

func (s *scheduleSnapshot) captureArchetypeDirectory(ctx context.Context, root *os.Root, dirs []string, i, total int) ([]string, int, error) {
	entries, err := readPublicDirectory(root, dirs[i])
	if err != nil {
		return dirs, total, err
	}
	for j := 0; j < len(entries) && j < maxPublicTreeEntries; j++ {
		rel := filepath.Join(dirs[i], entries[j].Name())
		info, err := root.Lstat(rel)
		if err != nil {
			return dirs, total, err
		}
		if info.IsDir() {
			dirs = append(dirs, rel)
		} else {
			data, err := readScheduleFile(ctx, root, rel, 1<<20, false)
			if err != nil {
				return dirs, total, err
			}
			s.files[filepath.ToSlash(filepath.Join("source/.config/archetypes", rel))] = data
			total += len(data)
		}
		if len(dirs)+len(s.files) > 512 || total > maxScheduleInputBytes {
			return dirs, total, errors.New("source bundle snapshot exceeds 512 entries or 16 MiB")
		}
	}
	return dirs, total, nil
}
