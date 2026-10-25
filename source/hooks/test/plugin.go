package test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Plugin is a copy of the plugin's launcher and shims in a temporary tree,
// with a fake curl serving releases from a directory instead of the network.
type Plugin struct {
	Root     string
	Data     string
	Config   string
	Releases string
	Tools    string
	CurlLog  string
	Version  string
}

// platform is the suffix of the release archive hooks/run picks on this machine.
func platform() string {
	return runtime.GOOS + "_" + runtime.GOARCH
}

func NewPlugin(t *testing.T, source string, version string) *Plugin {
	t.Helper()

	base := t.TempDir()
	plugin := &Plugin{
		Root:     filepath.Join(base, "plugin"),
		Data:     filepath.Join(base, "data"),
		Config:   filepath.Join(base, "config"),
		Releases: filepath.Join(base, "releases"),
		Tools:    filepath.Join(base, "tools"),
		CurlLog:  filepath.Join(base, "curl.log"),
		Version:  version,
	}

	for _, script := range []string{"hooks/run", "bin/agentic-capture", "bin/agentic-memory"} {
		content, err := os.ReadFile(filepath.Join(source, script))
		if err != nil {
			t.Fatalf("reading %s: %v", script, err)
		}

		writeExecutable(t, filepath.Join(plugin.Root, script), string(content))
	}

	WriteFile(t, filepath.Join(plugin.Root, ".claude-plugin", "plugin.json"),
		fmt.Sprintf("{\n  \"name\": \"agentic-helpers\",\n  \"version\": %q\n}\n", version))
	writeExecutable(t, filepath.Join(plugin.Tools, "curl"), Fixture(t, "plugin/curl"))

	return plugin
}

// Binaries puts a fake binary under each name into dir; it prints its own
// path and arguments.
func (plugin *Plugin) Binaries(t *testing.T, dir string, names ...string) {
	t.Helper()

	for _, name := range names {
		writeExecutable(t, filepath.Join(dir, name), Fixture(t, "plugin/binary"))
	}
}

func (plugin *Plugin) Installed(t *testing.T, version string, names ...string) {
	t.Helper()

	plugin.Binaries(t, filepath.Join(plugin.Data, version), names...)
}

// Release publishes version for this machine: an archive holding a fake binary
// under each name, and a checksums.txt whose line for it carries sum, or the
// archive's real sha256 where sum is empty.
func (plugin *Plugin) Release(t *testing.T, version string, sum string, names ...string) {
	t.Helper()

	var packed bytes.Buffer

	zipped := gzip.NewWriter(&packed)
	archive := tar.NewWriter(zipped)
	binary := Fixture(t, "plugin/binary")

	for _, name := range names {
		header := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(binary)), Typeflag: tar.TypeReg}

		err := archive.WriteHeader(header)
		if err != nil {
			t.Fatalf("packing %s: %v", name, err)
		}

		_, err = archive.Write([]byte(binary))
		if err != nil {
			t.Fatalf("packing %s: %v", name, err)
		}
	}

	err := errors.Join(archive.Close(), zipped.Close())
	if err != nil {
		t.Fatalf("closing the archive: %v", err)
	}

	name := fmt.Sprintf("agentic-helpers_%s_%s.tar.gz", version, platform())
	if sum == "" {
		digest := sha256.Sum256(packed.Bytes())
		sum = hex.EncodeToString(digest[:])
	}

	dir := filepath.Join(plugin.Releases, "v"+version)
	WriteFile(t, filepath.Join(dir, name), packed.String())
	WriteFile(t, filepath.Join(dir, "checksums.txt"), fmt.Sprintf("%s  %s\n", sum, name))
}

type Ran struct {
	Code   int
	Stdout string
	Stderr string
}

// Run launches the script at path under the plugin root with the environment
// a hook gets; extra holds NAME=value pairs added to it, and an entry naming a
// variable with no value removes it.
func (plugin *Plugin) Run(t *testing.T, script string, extra []string, args ...string) Ran {
	t.Helper()

	command := plugin.command(script, extra, args)

	var stdout, stderr bytes.Buffer

	command.Stdout = &stdout
	command.Stderr = &stderr

	err := command.Run()

	var exited *exec.ExitError
	if err != nil && !errors.As(err, &exited) {
		t.Fatalf("running %s: %v", script, err)
	}

	return Ran{Code: command.ProcessState.ExitCode(), Stdout: stdout.String(), Stderr: stderr.String()}
}

// Start launches the script as Run does and returns at once, for a test that
// signals the launcher mid-run.
func (plugin *Plugin) Start(t *testing.T, script string, extra []string, args ...string) *exec.Cmd {
	t.Helper()

	command := plugin.command(script, extra, args)

	err := command.Start()
	if err != nil {
		t.Fatalf("starting %s: %v", script, err)
	}

	return command
}

func (plugin *Plugin) command(script string, extra []string, args []string) *exec.Cmd {
	env := map[string]string{
		"PATH":               plugin.Tools + ":/usr/bin:/bin:/usr/sbin:/sbin",
		"HOME":               filepath.Join(filepath.Dir(plugin.Root), "home"),
		"CLAUDE_PLUGIN_ROOT": plugin.Root,
		"CLAUDE_PLUGIN_DATA": plugin.Data,
		"FAKE_RELEASES":      plugin.Releases,
		"FAKE_CURL_LOG":      plugin.CurlLog,
	}

	for _, pair := range extra {
		name, value, found := strings.Cut(pair, "=")
		if found {
			env[name] = value
		} else {
			delete(env, name)
		}
	}

	command := exec.Command(filepath.Join(plugin.Root, script), args...)
	command.Dir = plugin.Root

	for name, value := range env {
		command.Env = append(command.Env, name+"="+value)
	}

	return command
}

func (plugin *Plugin) CurlCalls(t *testing.T) []string {
	t.Helper()

	content, err := os.ReadFile(plugin.CurlLog)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		t.Fatalf("reading the curl log: %v", err)
	}

	return strings.Fields(string(content))
}

func writeExecutable(t *testing.T, path string, content string) {
	t.Helper()

	WriteFile(t, path, content)

	err := os.Chmod(path, 0o755)
	if err != nil {
		t.Fatalf("making %s executable: %v", path, err)
	}
}
