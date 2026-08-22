// Copyright 2026 The Bazel Authors. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ibazel

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bazelbuild/bazel-watcher/internal/bazel"
	"github.com/bazelbuild/bazel-watcher/internal/ibazel/fswatcher/common"
	"github.com/bazelbuild/bazel-watcher/internal/ibazel/log"
	analysispb "github.com/bazelbuild/bazel-watcher/third_party/bazel/master/src/main/protobuf/analysis"
	blaze_query "github.com/bazelbuild/bazel-watcher/third_party/bazel/master/src/main/protobuf/blaze_query"
	"github.com/golang/protobuf/proto"
)

type benchmarkWorkspace struct{ path string }

func (w benchmarkWorkspace) FindWorkspace() (string, error)  { return w.path, nil }
func (w benchmarkWorkspace) ExecuteCommand(string, []string) {}

type benchmarkBazel struct {
	bazel.Bazel
	args        []string
	info        map[string]string
	cqueries    int
	queries     int
	infos       int
	repoMapping int
}

func (b *benchmarkBazel) Args() []string                        { return b.args }
func (b *benchmarkBazel) SetArguments(args []string)            { b.args = args }
func (b *benchmarkBazel) SetStartupArgs([]string)               {}
func (b *benchmarkBazel) WriteToStderr(bool)                    {}
func (b *benchmarkBazel) WriteToStdout(bool)                    {}
func (b *benchmarkBazel) Cancel()                               {}
func (b *benchmarkBazel) Test(...string) (*bytes.Buffer, error) { return nil, nil }

func (b *benchmarkBazel) Info() (map[string]string, *bytes.Buffer, error) {
	b.infos++
	return b.info, nil, nil
}

func (b *benchmarkBazel) DumpRepoMapping(string) (map[string]string, *bytes.Buffer, error) {
	b.repoMapping++
	return map[string]string{}, nil, nil
}

func (b *benchmarkBazel) CQuery(args ...string) (*analysispb.CqueryResult, error) {
	b.cqueries++
	target := args[0]
	if strings.HasPrefix(target, "kind('source file'") {
		return &analysispb.CqueryResult{Results: []*analysispb.ConfiguredTarget{
			benchmarkSource("//app:source.ts"),
		}}, nil
	}
	if strings.HasPrefix(target, "deps(set(") {
		return &analysispb.CqueryResult{Results: []*analysispb.ConfiguredTarget{
			benchmarkRule("//app:target"),
			benchmarkSource("//app:source.ts"),
		}}, nil
	}
	return &analysispb.CqueryResult{Results: []*analysispb.ConfiguredTarget{
		benchmarkRule(target),
	}}, nil
}

func (b *benchmarkBazel) Query(...string) (*blaze_query.QueryResult, error) {
	b.queries++
	return &blaze_query.QueryResult{Target: []*blaze_query.Target{{
		Type:       blaze_query.Target_SOURCE_FILE.Enum(),
		SourceFile: &blaze_query.SourceFile{Name: proto.String("//app:BUILD")},
	}}}, nil
}

func benchmarkRule(name string) *analysispb.ConfiguredTarget {
	return &analysispb.ConfiguredTarget{Target: &blaze_query.Target{
		Type: blaze_query.Target_RULE.Enum(),
		Rule: &blaze_query.Rule{Name: proto.String(name)},
	}}
}

func benchmarkSource(name string) *analysispb.ConfiguredTarget {
	return &analysispb.ConfiguredTarget{Target: &blaze_query.Target{
		Type:       blaze_query.Target_SOURCE_FILE.Enum(),
		SourceFile: &blaze_query.SourceFile{Name: proto.String(name)},
	}}
}

func newBenchmarkIBazel(b *testing.B) (*IBazel, *benchmarkBazel) {
	b.Helper()
	root := b.TempDir()
	app := filepath.Join(root, "app")
	external := filepath.Join(root, "external")
	for _, path := range []string{app, external} {
		if err := os.Mkdir(path, 0o700); err != nil {
			b.Fatal(err)
		}
	}
	for _, name := range []string{"BUILD", "source.ts"} {
		if err := os.WriteFile(filepath.Join(app, name), nil, 0o600); err != nil {
			b.Fatal(err)
		}
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(app, filepath.Join(external, "repo")); err != nil {
			b.Fatal(err)
		}
	}

	fake := &benchmarkBazel{info: map[string]string{
		"output_base": root, "install_base": filepath.Join(root, "install"),
	}}
	previousBazelNew := bazelNew
	bazelNew = func() bazel.Bazel { return fake }
	log.SetLogger(log.NewWriterLogger(io.Discard))
	b.Cleanup(func() {
		bazelNew = previousBazelNew
		log.SetLogger(log.NewWriterLogger(os.Stderr))
	})

	return &IBazel{
		workspaceFinder:   benchmarkWorkspace{path: root},
		buildFileWatcher:  &fakeFSNotifyWatcher{},
		sourceFileWatcher: &fakeFSNotifyWatcher{},
		filesWatched:      map[common.Watcher]map[string]struct{}{},
	}, fake
}

func reportBazelCommands(b *testing.B, fake *benchmarkBazel) {
	b.Helper()
	operations := float64(b.N)
	b.ReportMetric(float64(fake.cqueries)/operations, "cquery/op")
	b.ReportMetric(float64(fake.queries)/operations, "query/op")
	b.ReportMetric(float64(fake.infos)/operations, "info/op")
	b.ReportMetric(float64(fake.repoMapping)/operations, "repo_mapping/op")
}

func BenchmarkIBazelWatchDiscovery(b *testing.B) {
	i, fake := newBenchmarkIBazel(b)
	target := "//app:target"
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		i.state = QUERY
		i.iteration("build", i.build, []string{target}, target)
	}
	b.StopTimer()
	reportBazelCommands(b, fake)
}

func BenchmarkIBazelTest(b *testing.B) {
	for _, explicitOutput := range []bool{false, true} {
		name := "repeated"
		if explicitOutput {
			name = "explicit_output"
		}
		b.Run(name, func(b *testing.B) {
			i, fake := newBenchmarkIBazel(b)
			targets := []string{"//app:first", "//app:second"}
			if explicitOutput {
				i.SetBazelArgs([]string{"--test_output=summary"})
			}
			b.ReportAllocs()
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				if _, err := i.test(targets...); err != nil {
					b.Fatal(fmt.Errorf("test run failed: %w", err))
				}
			}
			b.StopTimer()
			reportBazelCommands(b, fake)
		})
	}
}
